package engine

import (
	"context"
	"fmt"
	"log"
	"time"
	"net/url"
	"strings"
	"encoding/json"

	"p2c-engine/internal/p2c"
)

// Worker is a stub that will later connect to P2C and process orders.
type Worker struct {
	cfg         WorkerConfig
	stopCh      chan struct{}
	doneCh      chan struct{}
	client      *p2c.Client
	botToken    string
	cursor      string
	seen        map[string]time.Time
	reqHistory  []time.Time
	cancel      context.CancelFunc
	p2cAccountID string
	penaltyUntil time.Time
	penaltyReason string
	takeMap     map[string]int64 // hex -> numeric id
}

type WorkerConfig struct {
	AccountID   int64
	AccessToken string
	ChatID      int64
	MinAmount   *float64
	MaxAmount   *float64
	AutoMode    bool
	Active      bool
	P2CAccountID string
}

func NewWorker(cfg WorkerConfig, client *p2c.Client, botToken string) *Worker {
	return &Worker{
		cfg:      cfg,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		client:   client,
		botToken: botToken,
		seen:     make(map[string]time.Time),
		p2cAccountID: cfg.P2CAccountID,
		takeMap:  make(map[string]int64),
	}
}

func (w *Worker) Start() {
	go func() {
		defer close(w.doneCh)
		log.Printf("[worker %d] start (active=%v auto=%v)", w.cfg.AccountID, w.cfg.Active, w.cfg.AutoMode)
		if !w.cfg.Active || !w.cfg.AutoMode {
			log.Printf("[worker %d] stopped (inactive/auto off)", w.cfg.AccountID)
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		w.cancel = cancel
		for {
			if err := p2c.SubscribeSocket(ctx, w.client.BaseURL(), w.cfg.AccessToken, w.handleLivePayment); err != nil {
				log.Printf("[worker %d] websocket error: %v", w.cfg.AccountID, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				log.Printf("[worker %d] reconnecting...", w.cfg.AccountID)
			}
		}
	}()
}

func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	close(w.stopCh)
	<-w.doneCh
}

// TakeOrder is a stub for manual mode; will later hit P2C API.
func (w *Worker) TakeOrder(_ context.Context, externalID string) error {
	log.Printf("[worker %d] received request to take order %s (stub)", w.cfg.AccountID, externalID)
	return nil
}

// CompletePayment confirms payment in manual mode.
func (w *Worker) CompletePayment(ctx context.Context, paymentID string) error {
	if w.p2cAccountID == "" {
		return fmt.Errorf("no p2c account id configured")
	}
	// если paymentID в hex, попробуем найти numeric id
	if num, ok := w.takeMap[paymentID]; ok {
		paymentID = fmt.Sprintf("%d", num)
	}
	if err := w.client.CompletePayment(ctx, paymentID, w.p2cAccountID); err != nil {
		return err
	}
	return nil
}
func (w *Worker) pollOnce(t time.Time) {
	if w.client == nil {
		return
	}
	if !w.cfg.Active || !w.cfg.AutoMode {
		return
	}

	if !w.allowRequest(t) {
		log.Printf("[worker %d] poll skipped: rate limit window", w.cfg.AccountID)
		return
	}

	// release active lock after 30s to avoid perma-block
	payments, err := w.client.ListPayments(context.Background(), p2c.ListPaymentsParams{
		Size:   10,
		Status: p2c.StatusProcessing,
		Cursor: w.cursor,
		// статус не фильтруем, смотрим все и логируем
	})
	if err != nil {
		log.Printf("[worker %d] poll error: %v", w.cfg.AccountID, err)
		return
	}
	if len(payments.Data) == 0 {
		log.Printf("[worker %d] poll: empty", w.cfg.AccountID)
		return
	}

	if payments.Cursor != "" {
		w.cursor = payments.Cursor
	}

	now := time.Now()
	w.evictSeen(now)

	for _, p := range payments.Data {
		if _, ok := w.seen[p.IDString()]; ok {
			continue
		}
		w.seen[p.IDString()] = now

		log.Printf(
			"[worker %d] seen payment id=%s status=%s amount=%s %s",
			w.cfg.AccountID, p.IDString(), p.Status, p.AmountFiat, p.Fiat,
		)

		// пропускаем явно завершенные/отмененные
		if p.Status == p2c.StatusCompleted || p.Status == p2c.StatusDisputed || p.Status == p2c.StatusCanceled || p.Status == p2c.StatusRefunded {
			continue
		}

		amountFiat := p.AmountFiatValue()
		if w.cfg.MinAmount != nil && amountFiat < *w.cfg.MinAmount {
			log.Printf("[worker %d] skip %s: below min %.2f < %.2f", w.cfg.AccountID, p.ID, amountFiat, *w.cfg.MinAmount)
			continue
		}
		if w.cfg.MaxAmount != nil && amountFiat > *w.cfg.MaxAmount {
			log.Printf("[worker %d] skip %s: above max %.2f > %.2f", w.cfg.AccountID, p.ID, amountFiat, *w.cfg.MaxAmount)
			continue
		}

		log.Printf("[worker %d] trying take payment %s amount=%.2f %s", w.cfg.AccountID, p.IDString(), amountFiat, p.Fiat)
		if err := w.client.TakePayment(context.Background(), p.IDString()); err != nil {
			log.Printf("[worker %d] take payment %s error: %v", w.cfg.AccountID, p.IDString(), err)
			w.sendTelegram(buildMessage(p, false, err.Error()))
			continue
		}

		log.Printf("[worker %d] took payment %s amount=%.2f %s", w.cfg.AccountID, p.IDString(), amountFiat, p.Fiat)
		w.sendTelegram(buildMessage(p, true, ""))
		break // берем по одной
	}
}

func (w *Worker) sendTelegram(text string) {
	if w.botToken == "" {
		log.Printf("[worker %d] skip tg send: empty bot token", w.cfg.AccountID)
		return
	}
	if w.cfg.ChatID == 0 {
		log.Printf("[worker %d] skip tg send: chat_id=0", w.cfg.AccountID)
		return
	}
	if err := sendMessage(w.botToken, w.cfg.ChatID, text); err != nil {
		log.Printf("[worker %d] telegram send error: %v", w.cfg.AccountID, err)
	}
}

func (w *Worker) sendTelegramPhoto(photoURL, caption string, markup map[string]any) error {
	if w.botToken == "" {
		log.Printf("[worker %d] skip tg send: empty bot token", w.cfg.AccountID)
		return fmt.Errorf("empty bot token")
	}
	if w.cfg.ChatID == 0 {
		log.Printf("[worker %d] skip tg send: chat_id=0", w.cfg.AccountID)
		return fmt.Errorf("empty chat")
	}
	return sendPhoto(w.botToken, w.cfg.ChatID, photoURL, caption, markup)
}

func (w *Worker) evictSeen(now time.Time) {
	ttl := 10 * time.Minute
	for id, ts := range w.seen {
		if now.Sub(ts) > ttl {
			delete(w.seen, id)
		}
	}
}

// allowRequest делает простое скользящее окно 5 минут для запросов к API, чтобы не превысить порог.
func (w *Worker) allowRequest(now time.Time) bool {
	window := 5 * time.Minute
	limit := 180 // чуть ниже 200 за 5 минут

	// очистка окна
	idx := 0
	for _, ts := range w.reqHistory {
		if now.Sub(ts) <= window {
			break
		}
		idx++
	}
	if idx > 0 && idx < len(w.reqHistory) {
		w.reqHistory = w.reqHistory[idx:]
	} else if idx >= len(w.reqHistory) {
		w.reqHistory = w.reqHistory[:0]
	}

	if len(w.reqHistory) >= limit {
		return false
	}
	w.reqHistory = append(w.reqHistory, now)
	return true
}

func (w *Worker) handleLivePayment(p p2c.LivePayment) {
	if _, ok := w.seen[p.ID]; ok {
		return
	}
	w.seen[p.ID] = time.Now()

	// Если есть актуальный блок, не трогаем заявки
	if time.Now().Before(w.penaltyUntil) {
		log.Printf("[worker %d] skip %s: in penalty until %s", w.cfg.AccountID, p.ID, w.penaltyUntil.Format(time.RFC3339))
		return
	}

	log.Printf("[worker %d] live add id=%s amount=%s rate=%s", w.cfg.AccountID, p.ID, p.InAmount, p.ExchangeRate)

	resp, err := w.client.TakeLivePayment(context.Background(), p.ID)
	if err != nil {
		if until, reason, ok := parsePenalty(err); ok {
			w.penaltyUntil = until
			w.penaltyReason = reason
			msg := fmt.Sprintf("⛔️ Блок до %s\nПричина: %s\nЗаявки временно не принимаем.", until.Local().Format("15:04:05"), reason)
			w.sendTelegram(msg)
			log.Printf("[worker %d] penalty until %s reason=%s", w.cfg.AccountID, until.Format(time.RFC3339), reason)
		} else if isActiveExists(err) {
			log.Printf("[worker %d] active order exists, skip take", w.cfg.AccountID)
		} else {
			log.Printf("[worker %d] take %s error: %v", w.cfg.AccountID, p.ID, err)
		}
		return
	}
	if resp != nil {
		defer fasthttp.ReleaseResponse(resp)
		var tr p2c.TakeResponse
		if err := json.Unmarshal(resp.Body(), &tr); err == nil && tr.Data != nil {
			if num, err := tr.Data.ID.Int64(); err == nil {
				w.takeMap[p.ID] = num
			}
		}
	}

	// Успешно приняли — отправляем в ТГ.
	status := "🤖 Заявка принята автоматически ✅"
	qrURL := fmt.Sprintf("https://quickchart.io/qr?text=%s&size=300", urlEncode(p.URL))
	caption := buildLiveCaption(p, status)
	if err := w.sendTelegramPhoto(qrURL, caption, buildPaidKeyboard(w.cfg.AccountID, p)); err != nil {
		log.Printf("[worker %d] telegram photo error: %v", w.cfg.AccountID, err)
		w.sendTelegram(caption)
	}
}

func urlEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

type penaltyPayload struct {
	Error        string `json:"error"`
	PenaltyEndAt string `json:"penalty_end_at"`
	PenaltyType  string `json:"penalty_type"`
}

func parsePenalty(err error) (time.Time, string, bool) {
	if err == nil {
		return time.Time{}, "", false
	}
	var payload penaltyPayload
	if json.Unmarshal([]byte(err.Error()), &payload) == nil {
		if payload.Error == "MerchantPenalized" && payload.PenaltyEndAt != "" {
			t, _ := time.Parse(time.RFC3339, payload.PenaltyEndAt)
			return t, payload.PenaltyType, true
		}
	}
	// fallback: try find substring penalty_end_at
	if strings.Contains(err.Error(), "MerchantPenalized") {
		// very naive parse
		if idx := strings.Index(err.Error(), "penalty_end_at"); idx >= 0 {
			rest := err.Error()[idx:]
			if q := strings.Index(rest, "\""); q >= 0 {
				rest = rest[q+1:]
				if q2 := strings.Index(rest, "\""); q2 >= 0 {
					ts := rest[:q2]
					t, _ := time.Parse(time.RFC3339, ts)
					return t, "unknown", true
				}
			}
		}
	}
	return time.Time{}, "", false
}

func isActiveExists(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "ActiveOrderExists")
}
