package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/moderation"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/server"
	"github.com/thiagohmm/tiktok-live-monitor/internal/signer"
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

	db, err := database.Open(baseDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()
	log.Println("Database initialized.")

	aiMgr := ai.NewManager(modelsDir, binDir)
	defer aiMgr.Stop()

	// Start local signer.
	signerSvc := signer.New()
	signerCtx, signerCancel := context.WithCancel(context.Background())
	defer signerCancel()
	go func() {
		if err := signerSvc.Start(signerCtx); err != nil {
			log.Printf("[Main] Signer stopped: %v", err)
		}
	}()

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := signerSvc.WaitReady(readyCtx); err != nil {
		log.Fatalf("Failed to start local signer: %v", err)
	}
	os.Setenv("TIKTOK_SIGNER_URL", signerSvc.BaseURL())
	log.Printf("Local signer ready at %s", signerSvc.BaseURL())

	mon, err := monitor.New()
	if err != nil {
		log.Fatalf("Failed to create monitor: %v", err)
	}

	modEngine := moderation.NewEngine(aiMgr, db)

	srv := server.New(server.Config{
		Host:      os.Getenv("HOST"),
		Port:      3000,
		ModelsDir: modelsDir,
		BinDir:    binDir,
		WebDir:    filepath.Join(baseDir, "web"),
	}, db, aiMgr, modEngine, mon)

	ctx := context.Background()
	log.Println("Starting TikTok Live Monitor (Go backend)...")
	log.Println("Open http://localhost:3000 in your browser")
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
