//go:build wails
// +build wails

// Wails desktop entry point for TikTok Live Monitor.
// Starts the HTTP backend and opens a native webview window.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/moderation"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/server"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[tiktok-live-monitor] ")

	// Determine base directory.
	exePath, _ := os.Executable()
	baseDir := filepath.Dir(exePath)

	// In dev mode (wails3 dev), use CWD.
	if _, err := os.Stat(filepath.Join(baseDir, "web")); os.IsNotExist(err) {
		baseDir, _ = os.Getwd()
	}

	// If running from cmd subdirectory, go up.
	if filepath.Base(baseDir) == "cmd" {
		baseDir = filepath.Dir(baseDir)
	}

	log.Printf("Base directory: %s", baseDir)

	modelsDir := filepath.Join(baseDir, "models")
	binDir := filepath.Join(baseDir, "bin")

	// Initialize database.
	db, err := database.Open(baseDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Initialize AI manager.
	aiMgr := ai.NewManager(modelsDir, binDir)
	defer aiMgr.Stop()

	// Initialize monitor.
	mon, err := monitor.New()
	if err != nil {
		log.Fatalf("Failed to create monitor: %v", err)
	}

	// Initialize moderation engine.
	modEngine := moderation.NewEngine(aiMgr, db)

	// Start HTTP server in background.
	httpServer := server.New(server.Config{
		Host:      "127.0.0.1",
		Port:      3000,
		ModelsDir: modelsDir,
		BinDir:    binDir,
		WebDir:    filepath.Join(baseDir, "web"),
	}, db, aiMgr, modEngine, mon)

	go func() {
		log.Println("Starting HTTP backend on http://127.0.0.1:3000")
		if err := httpServer.Start(context.Background()); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// Wait for HTTP server to be ready.
	waitForServer("http://127.0.0.1:3000/api/state", 5*time.Second)

	// Create Wails desktop app.
	app := application.New(application.Options{
		Name:        "TikTok Live Monitor",
		Description: "Monitor de lives TikTok com moderação IA",
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyRegular,
		},
	})

	// Create main window pointing to the HTTP backend.
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "TikTok Live Monitor",
		Width:  1200,
		Height: 900,
		URL:    "http://127.0.0.1:3000",
	})

	log.Println("Starting Wails desktop app...")
	if err := app.Run(); err != nil {
		log.Fatalf("Wails app error: %v", err)
	}
}

func waitForServer(url string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Println("Warning: HTTP server not ready within timeout, launching window anyway")
}
