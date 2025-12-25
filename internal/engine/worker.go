package engine

import (
	"context"
	"log"
	"time"

	"p2c-engine/internal/p2c"
)

// Worker is a stub that will later connect to P2C and process orders.
type Worker struct {
	accountID int64
	stopCh    chan struct{}
	doneCh    chan struct{}
	client    *p2c.Client
}

func NewWorker(accountID int64, client *p2c.Client) *Worker {
	return &Worker{
		accountID: accountID,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		client:    client,
	}
}

func (w *Worker) Start() {
	go func() {
		defer close(w.doneCh)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				log.Printf("[worker %d] stopped", w.accountID)
				return
			case t := <-ticker.C:
				// Placeholder: poll payments endpoint as a stub.
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
	log.Printf("[worker %d] received request to take order %s (stub)", w.accountID, externalID)
	return nil
}

func (w *Worker) pollOnce(t time.Time) {
	if w.client == nil {
		return
	}
	payments, err := w.client.ListPayments(context.Background(), p2c.ListPaymentsParams{
		Size:   5,
		Status: p2c.StatusProcessing,
	})
	if err != nil {
		log.Printf("[worker %d] poll error: %v", w.accountID, err)
		return
	}
	if len(payments.Data) > 0 {
		log.Printf("[worker %d] %d payments at %s", w.accountID, len(payments.Data), t.Format(time.RFC3339))
	}
}
