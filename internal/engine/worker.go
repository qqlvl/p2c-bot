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
	}
}

func (w *Worker) Start() {
	go func() {
		defer close(w.doneCh)
		// Опрашиваем не чаще, чем ~2 сек, чтобы укладываться в лимит ~40 запросов/мин.
		ticker := time.NewTicker(2 * time.Second)
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

	// release active lock after 30s to avoid perma-block
	if w.hasActive && time.Now().After(w.activeUntil) {
		w.hasActive = false
	}
	if w.hasActive {
		return
	}

	payments, err := w.client.ListPayments(context.Background(), p2c.ListPaymentsParams{
		Size:   5,
		Status: p2c.StatusProcessing,
	})
	if err != nil {
		log.Printf("[worker %d] poll error: %v", w.cfg.AccountID, err)
		return
	}

	for _, p := range payments.Data {
		amountFiat := p.AmountFiatValue()
		if w.cfg.MinAmount != nil && amountFiat < *w.cfg.MinAmount {
			log.Printf("[worker %d] skip %s: below min %.2f < %.2f", w.cfg.AccountID, p.ID, amountFiat, *w.cfg.MinAmount)
			continue
		}
		if w.cfg.MaxAmount != nil && amountFiat > *w.cfg.MaxAmount {
			log.Printf("[worker %d] skip %s: above max %.2f > %.2f", w.cfg.AccountID, p.ID, amountFiat, *w.cfg.MaxAmount)
			continue
		}

		log.Printf("[worker %d] trying take payment %s amount=%.2f %s", w.cfg.AccountID, p.ID, amountFiat, p.Fiat)
		if err := w.client.TakePayment(context.Background(), p.ID); err != nil {
			log.Printf("[worker %d] take payment %s error: %v", w.cfg.AccountID, p.ID, err)
			w.sendTelegram(buildMessage(p, false, err.Error()))
			continue
		}

		w.hasActive = true
		w.activeUntil = time.Now().Add(30 * time.Second)
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
