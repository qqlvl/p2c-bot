package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"p2c-engine/internal/engine"
	"p2c-engine/internal/httpserver"
	"p2c-engine/internal/p2c"
)

func main() {
	addr := getenv("ENGINE_ADDR", ":8080")
	baseURL := getenv("P2C_BASE_URL", "https://app.cr.bot/internal/v1")
	// Prefer dedicated engine token, but fall back to bot token if not provided.
	botToken := getenv("P2C_BOT_TOKEN", os.Getenv("BOT_TOKEN"))

	p2cClient := p2c.NewClient(baseURL, "")
	mgr := engine.NewManager(p2cClient, botToken)
	srv := httpserver.New(addr, mgr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("p2c-engine HTTP listening on %s", addr)
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		} else {
			log.Printf("server stopped: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutdown signal received, stopping...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	mgr.StopAll()
	log.Println("p2c-engine stopped")
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
