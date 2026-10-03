package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/media"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/view"
	"github.com/thiagohmm/tiktok-live-monitor/internal/whatsapp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[tiktok-live-monitor] ")

	// Production authentication is local and fails closed without its database.
	if err := auth.CheckConfigFromEnv(); err != nil {
		log.Fatalf("Configuração de autenticação inválida: %v", err)
	}

	// Model layer: open the PostgreSQL repository.
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
	// The controller owns the monitor manager (one monitor per organization
	// and live; global cap in MAX_MONITORS, per-org cap in organizations).
	ctrl := controller.NewAppController(mon, repo)
	ctrl.SetMessageCache(msgCache)

	// Fila PIX: WAHA (WhatsApp) + MinIO (comprovantes). Best-effort: sem as
	// envs o recurso fica indisponível, mas o monitor de lives continua normal.
	wahaCfg := whatsapp.LoadConfigFromEnv()
	wahaClient := whatsapp.NewClient(wahaCfg)
	var mediaStore media.Store
	mediaCfg := media.LoadConfigFromEnv()
	if mediaCfg.Configured() {
		store, err := media.NewMinIOStorage(mediaCfg)
		if err != nil {
			log.Printf("Fila PIX: MinIO indisponível: %v", err)
		} else {
			mediaStore = store
			go func() {
				if err := store.EnsureBucket(context.Background()); err != nil {
					log.Printf("Fila PIX: não foi possível garantir o bucket MinIO: %v", err)
				}
			}()
		}
	}
	if mediaStore != nil {
		ctrl.SetPixQueueService(controller.NewPixQueueService(repo, wahaClient, mediaStore))
	}
	log.Printf("Fila PIX: %v", pixStatus(wahaCfg.Configured() && mediaStore != nil))

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

func pixStatus(ok bool) string {
	if ok {
		return "habilitada"
	}
	return "desabilitada (configure WAHA_URL/WAHA_API_KEY/WAHA_WEBHOOK_SECRET e MINIO_*)"
}
