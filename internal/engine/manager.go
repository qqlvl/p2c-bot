package engine

import (
	"context"
	"log"
	"sync"

	"p2c-engine/internal/p2c"
)

// Manager orchestrates account workers.
type Manager struct {
	mu      sync.Mutex
	workers map[int64]*Worker
	client  *p2c.Client
}

func NewManager(client *p2c.Client) *Manager {
	return &Manager{
		workers: make(map[int64]*Worker),
		client:  client,
	}
}

// ReloadAccount ensures a worker exists and restarts it with fresh settings (stub).
func (m *Manager) ReloadAccount(accountID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if w, ok := m.workers[accountID]; ok {
		w.Stop()
	}

	w := NewWorker(accountID, m.client)
	m.workers[accountID] = w
	w.Start()
}

// StopAll stops all workers.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, w := range m.workers {
		log.Printf("stopping worker for account %d", id)
		w.Stop()
	}
}

// TakeOrder delegates order taking to the worker (stubbed).
func (m *Manager) TakeOrder(ctx context.Context, accountID int64, externalID string) error {
	m.mu.Lock()
	w, ok := m.workers[accountID]
	m.mu.Unlock()
	if !ok {
		// If worker is absent, start it lazily.
		m.ReloadAccount(accountID)
		m.mu.Lock()
		w = m.workers[accountID]
		m.mu.Unlock()
	}
	return w.TakeOrder(ctx, externalID)
}
