package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/moderation"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/view"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[tiktok-live-monitor] ")

	baseDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get working directory: %v", err)
	}
	if filepath.Base(baseDir) == "cmd" || filepath.Base(baseDir) == "tiktok-live-monitor" {
		baseDir = filepath.Dir(baseDir)
	}
	log.Printf("Base directory: %s", baseDir)

	modelsDir := filepath.Join(baseDir, "models")
	binDir := filepath.Join(baseDir, "bin")

	// Model layer: open database repository
	repo, err := database.Open(baseDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer repo.Close()
	log.Println("Database initialized.")

	// Service layer: AI manager
	aiMgr := ai.NewManager(modelsDir, binDir)
	defer aiMgr.Stop()

	// Service layer: monitor
	mon, err := monitor.New()
	if err != nil {
		log.Fatalf("Failed to create monitor: %v", err)
	}

	// Service layer: moderation engine
	modEngine := moderation.NewEngine(aiMgr, repo)

	// In-memory write-behind cache for user messages (batched DB writes).
	// Registered after repo.Close so the final flush runs before the DB closes.
	msgCache := database.NewMessageCache(repo)
	msgCache.Start()
	defer msgCache.Stop()

	// Controller layer: orchestrate services
	ctrl := controller.NewAppController(aiMgr, modEngine, mon, repo)
	ctrl.SetMessageCache(msgCache)

	// View layer: HTTP server
	port := 3001
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	srv := view.New(view.Config{
		Host:      os.Getenv("HOST"),
		Port:      port,
		ModelsDir: modelsDir,
		BinDir:    binDir,
		WebDir:    filepath.Join(baseDir, "web"),
	}, ctrl)

	ctx := context.Background()
	log.Println("Starting TikTok Live Monitor (Go backend)...")
	log.Printf("Open http://localhost:%d in your browser", port)
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
