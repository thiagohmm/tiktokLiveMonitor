package view

import (
	"context"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// HTTPServer is the presentation layer (View) that handles HTTP requests.
type HTTPServer struct {
	httpServer    *http.Server
	controller    *controller.AppController
	sseClients    map[*sseClient]struct{}
	sseMu         sync.Mutex
	maxSSEClients int
	cfg           Config
	auth          auth.Config
	admin         *auth.AdminClient
	lockout       *auth.LoginLockout
	proxyTrust    auth.ProxyTrust
	theme         auth.ThemeColors
	corsOrigins   []string
}

// Config holds server configuration.
type Config struct {
	Host string
	Port int
}

// New creates a new HTTP server (View).
func New(cfg Config, ctrl *controller.AppController) *HTTPServer {
	authCfg := auth.LoadConfigFromEnv()
	return &HTTPServer{
		controller:    ctrl,
		sseClients:    make(map[*sseClient]struct{}),
		maxSSEClients: sseMaxClientsFromEnv(),
		cfg:           cfg,
		auth:          authCfg,
		admin:         auth.NewAdminClient(authCfg),
		lockout:       auth.NewLoginLockout(auth.LoadLockoutConfigFromEnv()),
		proxyTrust:    auth.LoadProxyTrustFromEnv(),
		theme:         auth.LoadThemeFromEnv(),
		corsOrigins:   LoadCORSOriginsFromEnv(),
	}
}

// Start begins listening and returns an error when the server stops.
func (s *HTTPServer) Start(ctx context.Context) error {
	port := 3001
	if s.cfg.Port > 0 {
		port = s.cfg.Port
	}
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	// Nota: não usamos debug.SetMaxThreads — o limite real é o do SO
	// (ex.: macOS kern.num_taskthreads=2048) e um teto interno só
	// anteciparia o fatal. A proteção contra muitos writers bloqueados é o
	// deadline curto por escrita (sseWriteTimeout): clientes presos são
	// ejetados em segundos.

	mux := http.NewServeMux()

	// SSE endpoint.
	mux.HandleFunc("/events", s.handleSSE)

	// API endpoints.
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/history", s.handleHistory)
	// Subárvore: DELETE /api/history/{id} (patterns sem barra só fazem match exato).
	mux.HandleFunc("/api/history/", s.handleHistory)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/clear-history", s.handleClearHistory)
	mux.HandleFunc("/api/readiness", s.handleReadiness)
	mux.HandleFunc("/api/gifts", s.handleGifts)
	mux.HandleFunc("/api/available-gifts", s.handleAvailableGifts)
	mux.HandleFunc("/api/target-gift-history", s.handleTargetGiftHistory)
	mux.HandleFunc("/api/target-gift-history/answer", s.handleTargetGiftHistoryAnswer)
	mux.HandleFunc("/api/pinned-comments", s.handlePinnedComments)
	mux.HandleFunc("/api/ranking", s.handleRanking)
	mux.HandleFunc("/api/report", s.handleReport)
	mux.HandleFunc("/api/profile", s.handleProfile)
	mux.HandleFunc("/api/goals", s.handleGoals)
	mux.HandleFunc("/api/goals/cancel", s.handleGoalCancel)
	mux.HandleFunc("/api/goals/complete", s.handleGoalComplete)
	mux.HandleFunc("/api/admin/lives", s.handleAdminLives)
	mux.HandleFunc("/api/admin/lives/delete", s.handleAdminLivesDelete)
	mux.HandleFunc("/api/auth/config", s.handleAuthConfig)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/signup", s.handleAuthSignup)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/auth/me", s.handleAuthMe)
	mux.HandleFunc("/api/admin/users", s.handleAdminUsers)
	mux.HandleFunc("/api/admin/users/update", s.handleAdminUsersUpdate)
	mux.HandleFunc("/api/admin/users/delete", s.handleAdminUsersDelete)

	// Rota raiz: apenas um aviso de que este é o backend (a UI vive em /frontend).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := fmt.Fprint(w, "tiktok-live-monitor backend OK — frontend em /frontend (nginx/Vercel/dev server).\n"); err != nil {
			log.Printf("[View] root route: %v", err)
		}
	})

	handler := s.auth.Middleware(mux)
	handler = s.cors(handler)
	handler = limitBody(handler)
	handler = securityHeaders(handler)

	// Setup monitor event handler via controller.
	s.controller.GetMonitor().OnEvent(func(eventType string, data monitor.EventData) {
		if eventType == monitor.EventAnyGift {
			go s.controller.HandleGiftEvent(data)
		}
		if eventType == monitor.EventChatMessage {
			go s.controller.HandleChatMessageEvent(data)
		}
		if eventType == monitor.EventNewSocialEvent {
			go s.controller.HandleShareEvent(data)
		}
		if eventType == monitor.EventNewLike {
			go s.controller.HandleLikeEvent(data)
		}
		if eventType == monitor.EventGiftUser {
			if id, err := s.controller.RecordTargetGiftReceived(data); err != nil {
				log.Printf("[View] Error recording target gift history: %v", err)
			} else {
				data["historyId"] = id
			}
		}
		if eventType == monitor.EventPinnedComment {
			if _, err := s.controller.RecordPinnedComment(data); err != nil {
				log.Printf("[View] Error recording pinned comment: %v", err)
			}
		}
		s.broadcastSSE(eventType, data)
	})

	// Goal progress updates (fired by the controller after gift events).
	s.controller.SetGoalCallback(func(update controller.GoalUpdate) {
		s.broadcastSSE("goal-update", update)
		if len(update.NewlyUnlockedMilestones) > 0 {
			s.broadcastSSE("goal-unlocked", update)
		}
		if update.Completed {
			s.broadcastSSE("goal-completed", update)
		}
	})

	addr := fmt.Sprintf("%s:%d", host, port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: handler,
		// Hardening against Slowloris and stuck connections. The SSE handler
		// clears the write/read deadlines per connection via ResponseController.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		select {
		case <-sigCh:
		case <-ctx.Done():
		}
		log.Println("[View] Shutting down...")
		s.controller.StopMonitoring()
		s.controller.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("[View] shutdown: %v", err)
		}
	}()

	log.Printf("[View] API running at http://%s:%d", host, port)
	return s.httpServer.ListenAndServe()
}

//
// Fan-out projetado para milhares de conexões simultâneas: cada cliente tem
// um canal próprio com buffer e uma goroutine de escrita dedicada. broadcastSSE
// enfileira de forma não-bloqueante; um cliente cujo buffer estoura (lento ou
// morto) é ejetado em vez de segurar o broadcast dos demais. Antes, um único
// lock global + escrita direta fazia UM cliente lento congelar TODOS os
// clientes (head-of-line blocking) e, sem deadline de escrita, um peer
// "blackholed" bloqueava o pipeline de eventos da live.
