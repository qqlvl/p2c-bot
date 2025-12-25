package engine

import (
	"context"
	"log"
	"time"
)

// Worker is a stub that will later connect to P2C and process orders.
type Worker struct {
	accountID int64
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func NewWorker(accountID int64) *Worker {
	return &Worker{
		accountID: accountID,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
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
				// Placeholder: will be replaced with listening to new orders.
				log.Printf("[worker %d] heartbeat at %s", w.accountID, t.Format(time.RFC3339))
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
