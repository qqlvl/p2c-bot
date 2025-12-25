package main

import (
	"context"
	"log"
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
	accessToken := os.Getenv("P2C_ACCESS_TOKEN")

	p2cClient := p2c.NewClient(baseURL, accessToken)
	mgr := engine.NewManager(p2cClient)
	srv := httpserver.New(addr, mgr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("p2c-engine HTTP listening on %s", addr)
		if err := srv.Start(); err != nil {
			log.Fatalf("server failed: %v", err)
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
