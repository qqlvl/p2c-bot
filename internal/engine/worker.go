package engine

import (
	"context"
	"log"
	"time"

	"p2c-engine/internal/p2c"
)

// Worker is a stub that will later connect to P2C and process orders.
type Worker struct {
	cfg         WorkerConfig
	stopCh      chan struct{}
	doneCh      chan struct{}
	client      *p2c.Client
	botToken    string
	hasActive   bool
	activeUntil time.Time
	cursor      string
	seen        map[string]time.Time
	reqHistory  []time.Time
}

type WorkerConfig struct {
	AccountID   int64
	AccessToken string
	ChatID      int64
	MinAmount   *float64
	MaxAmount   *float64
	AutoMode    bool
	Active      bool
}

func NewWorker(cfg WorkerConfig, client *p2c.Client, botToken string) *Worker {
	return &Worker{
		cfg:      cfg,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		client:   client,
		botToken: botToken,
		seen:     make(map[string]time.Time),
	}
}

func (w *Worker) Start() {
	go func() {
		defer close(w.doneCh)
		// Стартуем частый тикер, но дополнительно ограничиваем по окну 5 минут.
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				log.Printf("[worker %d] stopped", w.cfg.AccountID)
				return
			case t := <-ticker.C:
				w.pollOnce(t)
			}
		}
	}()
}

func (w *Worker) Stop() {
	close(w.stopCh)
	<-w.doneCh
}

// TakeOrder is a stub for manual mode; will later hit P2C API.
func (w *Worker) TakeOrder(_ context.Context, externalID string) error {
	log.Printf("[worker %d] received request to take order %s (stub)", w.cfg.AccountID, externalID)
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
	if w.hasActive && time.Now().After(w.activeUntil) {
		w.hasActive = false
	}
	if w.hasActive {
		return
	}

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

		w.hasActive = true
		w.activeUntil = time.Now().Add(30 * time.Second)
		log.Printf("[worker %d] took payment %s amount=%.2f %s", w.cfg.AccountID, p.IDString(), amountFiat, p.Fiat)
		w.sendTelegram(buildMessage(p, true, ""))
		break // берем по одной
	}
}

func (w *Worker) sendTelegram(text string) {
	if w.botToken == "" || w.cfg.ChatID == 0 {
		return
	}
	_ = sendMessage(w.botToken, w.cfg.ChatID, text)
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
