package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/view"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[tiktok-live-monitor] ")

	// Model layer: open the PostgreSQL (Supabase) repository.
	repo, err := database.OpenFromEnv()
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer func() {
		if err := repo.Close(); err != nil {
			log.Printf("Database close: %v", err)
		}
	}()
	log.Println("Database initialized.")

	// Service layer: monitor
	mon, err := monitor.New()
	if err != nil {
		log.Fatalf("Failed to create monitor: %v", err)
	}

	// In-memory write-behind cache for user messages (batched DB writes).
	// Registered after repo.Close so the final flush runs before the DB closes.
	msgCache := database.NewMessageCache(repo)
	msgCache.Start()
	defer msgCache.Stop()

	// Controller layer: orchestrate services
	ctrl := controller.NewAppController(mon, repo)
	ctrl.SetMessageCache(msgCache)

	// View layer: HTTP API server (SSE + REST). O frontend é servido
	// separadamente (frontend/) e faz proxy/rewrite para esta API.
	port := 3001
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	srv := view.New(view.Config{
		Host: os.Getenv("HOST"),
		Port: port,
	}, ctrl)

	ctx := context.Background()
	log.Println("Starting TikTok Live Monitor (Go API)...")
	log.Printf("API disponível em http://localhost:%d/api/readiness", port)
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
