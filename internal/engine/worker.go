package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"

	"p2c-engine/internal/p2c"
)

// Worker is a stub that will later connect to P2C and process orders.
type Worker struct {
	cfg         WorkerConfig
	stopCh      chan struct{}
	doneCh      chan struct{}
	client      *p2c.Client
	bgCtx       context.Context
	botToken    string
	cursor      string
	seen        map[string]time.Time
	reqHistory  []time.Time
	cancel      context.CancelFunc
	p2cAccountID string
	penaltyUntil time.Time
	penaltyReason string
	takeMap     map[string]int64 // hex -> numeric id
	activePaymentID string
	activeLockUntil time.Time
	lastPenaltyNotified time.Time
	mu sync.Mutex
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
		bgCtx:    context.Background(),
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
	hexID := paymentID
	if num, ok := w.lookupTakeID(paymentID); ok {
		paymentID = fmt.Sprintf("%d", num)
	}
	if err := w.client.CompletePayment(ctx, paymentID, w.p2cAccountID); err != nil {
		return err
	}
	w.clearActiveLock(hexID)
	return nil
}

// CancelPayment cancels accepted payment.
func (w *Worker) CancelPayment(ctx context.Context, paymentID string) error {
	if w.p2cAccountID == "" {
		return fmt.Errorf("no p2c account id configured")
	}
	hexID := paymentID
	if num, ok := w.lookupTakeID(paymentID); ok {
		paymentID = fmt.Sprintf("%d", num)
	}
	// P2C ожидает reason (enum). Используем допустимый вариант из фронта.
	const cancelReason = "balance"
	if err := w.client.CancelPayment(ctx, paymentID, cancelReason); err != nil {
		return err
	}
	w.clearActiveLock(hexID)
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
	payments, err := w.client.ListPayments(w.bgCtx, p2c.ListPaymentsParams{
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
	now := time.Now()
	w.seen[p.ID] = now

	start := time.Now()
	// Если уже есть активный ордер, не дергаем take, чтобы не ловить 400/ActiveOrderExists.
	if w.isActiveLocked(now) {
		log.Printf("[worker %d] skip %s: active order in progress", w.cfg.AccountID, p.ID)
		return
	}

	// Если есть актуальный блок, не трогаем заявки
	if now.Before(w.penaltyUntil) {
		return
	}

	// Фильтр по сумме
	if amount, err := strconv.ParseFloat(p.InAmount, 64); err == nil {
		if w.cfg.MinAmount != nil && amount < *w.cfg.MinAmount {
			log.Printf("[worker %d] skip %s: below min %.2f < %.2f", w.cfg.AccountID, p.ID, amount, *w.cfg.MinAmount)
			return
		}
		if w.cfg.MaxAmount != nil && *w.cfg.MaxAmount > 0 && amount > *w.cfg.MaxAmount {
			log.Printf("[worker %d] skip %s: above max %.2f > %.2f", w.cfg.AccountID, p.ID, amount, *w.cfg.MaxAmount)
			return
		}
	}

	resp, err := w.client.TakeLivePayment(w.bgCtx, p.ID)
	takeDur := time.Since(start)
	if err != nil {
		if until, reason, ok := parsePenalty(err); ok {
			w.penaltyUntil = until
			w.penaltyReason = reason
			if w.shouldNotifyPenalty(until) {
				msg := fmt.Sprintf("⛔️ Блок до %s\nПричина: %s\nЗаявки временно не принимаем.", until.Local().Format("15:04:05"), reason)
				w.sendTelegram(msg)
			}
		} else if isActiveExists(err) {
			w.bumpActiveLock()
		} else {
			log.Printf("[worker %d] take %s error in %dms: %v", w.cfg.AccountID, p.ID, takeDur.Milliseconds(), err)
		}
		return
	}
	w.setActiveLock(p.ID, p.ExpiresAt)

	var numericID int64
	if resp != nil {
		var tr p2c.TakeResponse
		if err := json.Unmarshal(resp.Body(), &tr); err == nil && tr.Data != nil {
			if num, err := tr.Data.ID.Int64(); err == nil {
				numericID = num
				w.storeTakeID(p.ID, num)
			}
		}
		fasthttp.ReleaseResponse(resp)
	}

	go w.notifyLiveAccepted(p, numericID)
	log.Printf("[worker %d] took %s amount=%s rate=%s in %dms", w.cfg.AccountID, p.ID, p.InAmount, p.ExchangeRate, takeDur.Milliseconds())
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

func (w *Worker) shouldNotifyPenalty(until time.Time) bool {
	if until.IsZero() {
		return false
	}
	if until.After(w.lastPenaltyNotified) {
		w.lastPenaltyNotified = until
		return true
	}
	return false
}

func (w *Worker) isActiveLocked(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.activeLockUntil.IsZero() && w.activePaymentID == "" {
		return false
	}
	if now.Before(w.activeLockUntil) {
		return true
	}
	w.activePaymentID = ""
	w.activeLockUntil = time.Time{}
	return false
}

func (w *Worker) setActiveLock(id string, expiresAt string) {
	lockUntil := time.Now().Add(5 * time.Minute)
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil && t.After(time.Now()) {
			lockUntil = t.Add(10 * time.Second)
		}
	}
	w.mu.Lock()
	w.activePaymentID = id
	w.activeLockUntil = lockUntil
	w.mu.Unlock()
}

func (w *Worker) bumpActiveLock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	backoff := time.Now().Add(2 * time.Second)
	if w.activeLockUntil.Before(backoff) {
		w.activeLockUntil = backoff
	}
}

func (w *Worker) clearActiveLock(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if id == "" || id == w.activePaymentID {
		w.activePaymentID = ""
		w.activeLockUntil = time.Time{}
	}
}

func (w *Worker) storeTakeID(hexID string, numericID int64) {
	if hexID == "" || numericID == 0 {
		return
	}
	w.mu.Lock()
	w.takeMap[hexID] = numericID
	w.mu.Unlock()
}

func (w *Worker) lookupTakeID(hexID string) (int64, bool) {
	if hexID == "" {
		return 0, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	num, ok := w.takeMap[hexID]
	return num, ok
}

func (w *Worker) notifyLiveAccepted(p p2c.LivePayment, numericID int64) {
	status := "🤖 Заявка принята автоматически ✅"
	qrURL := fmt.Sprintf("https://quickchart.io/qr?text=%s&size=200", urlEncode(p.URL))
	caption := buildLiveCaption(p, status)
	if err := w.sendTelegramPhoto(qrURL, caption, buildPaidKeyboard(w.cfg.AccountID, p)); err != nil {
		log.Printf("[worker %d] telegram photo error: %v", w.cfg.AccountID, err)
		w.sendTelegram(caption)
		return
	}
}
