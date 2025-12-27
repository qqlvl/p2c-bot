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
	botToken string
}

func NewManager(client *p2c.Client, botToken string) *Manager {
	return &Manager{
		workers: make(map[int64]*Worker),
		client:  client,
		botToken: botToken,
	}
}

// ReloadAccount ensures a worker exists and restarts it with fresh settings.
func (m *Manager) ReloadAccount(cfg WorkerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Если выключен аккаунт или авто-режим, гасим воркер и выходим.
	if !cfg.Active || !cfg.AutoMode {
		if w, ok := m.workers[cfg.AccountID]; ok {
			log.Printf("[mgr] stop account=%d active=%v auto=%v", cfg.AccountID, cfg.Active, cfg.AutoMode)
			w.Stop()
			delete(m.workers, cfg.AccountID)
		}
		return
	}

	// Перезапускаем с новыми настройками.
	if w, ok := m.workers[cfg.AccountID]; ok {
		w.Stop()
	}

	client := p2c.NewClient(m.client.BaseURL(), cfg.AccessToken)
	w := NewWorker(cfg, client, m.botToken)
	m.workers[cfg.AccountID] = w
	log.Printf("[mgr] reload account=%d active=%v auto=%v min=%.2f max=%.2f chat=%d", cfg.AccountID, cfg.Active, cfg.AutoMode, deref(cfg.MinAmount), deref(cfg.MaxAmount), cfg.ChatID)
	w.Start()
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
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
		m.ReloadAccount(WorkerConfig{AccountID: accountID})
		m.mu.Lock()
		w = m.workers[accountID]
		m.mu.Unlock()
	}
	return w.TakeOrder(ctx, externalID)
}

// CompletePayment delegates completion to worker.
func (m *Manager) CompletePayment(ctx context.Context, accountID int64, paymentID string) error {
	m.mu.Lock()
	w, ok := m.workers[accountID]
	m.mu.Unlock()
	if !ok {
		m.ReloadAccount(WorkerConfig{AccountID: accountID})
		m.mu.Lock()
		w = m.workers[accountID]
		m.mu.Unlock()
	}
	return w.CompletePayment(ctx, paymentID)
}

// CancelPayment delegates cancel to worker.
func (m *Manager) CancelPayment(ctx context.Context, accountID int64, paymentID string) error {
	m.mu.Lock()
	w, ok := m.workers[accountID]
	m.mu.Unlock()
	if !ok {
		m.ReloadAccount(WorkerConfig{AccountID: accountID})
		m.mu.Lock()
		w = m.workers[accountID]
		m.mu.Unlock()
	}
	return w.CancelPayment(ctx, paymentID)
}
