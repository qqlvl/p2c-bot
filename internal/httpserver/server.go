package httpserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"p2c-engine/internal/engine"
)

type Server struct {
	addr string
	mgr  *engine.Manager
	srv  *http.Server
}

func New(addr string, mgr *engine.Manager) *Server {
	s := &Server{
		addr: addr,
		mgr:  mgr,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/accounts/reload", s.handleReloadAccount)
	mux.HandleFunc("/orders/take", s.handleTakeOrder)

	s.srv = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) Start() error {
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReloadAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		AccountID   int64    `json:"account_id"`
		AccessToken string   `json:"access_token"`
		ChatID      int64    `json:"chat_id"`
		MinAmount   *float64 `json:"min_amount"`
		MaxAmount   *float64 `json:"max_amount"`
		AutoMode    *bool    `json:"auto_mode"`
		IsActive    *bool    `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	cfg := engine.WorkerConfig{
		AccountID:   req.AccountID,
		AccessToken: req.AccessToken,
		ChatID:      req.ChatID,
		MinAmount:   req.MinAmount,
		MaxAmount:   req.MaxAmount,
		AutoMode:    req.AutoMode != nil && *req.AutoMode,
		Active:      req.IsActive == nil || *req.IsActive,
	}
	s.mgr.ReloadAccount(cfg)
	writeJSON(w, http.StatusOK, map[string]any{"status": "reloaded", "ok": true})
}

func (s *Server) handleTakeOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		AccountID      int64  `json:"account_id"`
		OrderExternalID string `json:"order_external_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountID == 0 || req.OrderExternalID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := s.mgr.TakeOrder(r.Context(), req.AccountID, req.OrderExternalID); err != nil {
		log.Printf("take order error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
