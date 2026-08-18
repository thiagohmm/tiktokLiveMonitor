// Package view provides the HTTP server (View layer) with SSE and REST API for the TikTok Live Monitor.
package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/config"
	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// HTTPServer is the presentation layer (View) that handles HTTP requests.
type HTTPServer struct {
	httpServer *http.Server
	controller *controller.AppController
	sseClients map[http.ResponseWriter]bool
	sseMu      sync.Mutex
	webDir     string
}

// Config holds server configuration.
type Config struct {
	Host      string
	Port      int
	ModelsDir string
	BinDir    string
	WebDir    string
}

// New creates a new HTTP server (View).
func New(cfg Config, ctrl *controller.AppController) *HTTPServer {
	return &HTTPServer{
		controller: ctrl,
		sseClients: make(map[http.ResponseWriter]bool),
		webDir:     cfg.WebDir,
	}
}

// Start begins listening and returns an error when the server stops.
func (s *HTTPServer) Start(ctx context.Context) error {
	port := 3000
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	mux := http.NewServeMux()

	// SSE endpoint.
	mux.HandleFunc("/events", s.handleSSE)

	// API endpoints.
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/clear-history", s.handleClearHistory)
	mux.HandleFunc("/api/feedback", s.handleFeedback)
	mux.HandleFunc("/api/readiness", s.handleReadiness)
	mux.HandleFunc("/api/probe-llm", s.handleProbeLLM)
	mux.HandleFunc("/api/worker/register", s.handleWorkerRegister)
	mux.HandleFunc("/api/gifts", s.handleGifts)
	mux.HandleFunc("/api/available-gifts", s.handleAvailableGifts)
	mux.HandleFunc("/api/target-gift-history", s.handleTargetGiftHistory)
	mux.HandleFunc("/api/target-gift-history/answer", s.handleTargetGiftHistoryAnswer)
	mux.HandleFunc("/api/ask-ai", s.handleAskAI)

	// Static files.
	mux.Handle("/", http.FileServer(http.Dir(s.webDir)))

	// Chart.js vendor.
	mux.HandleFunc("/vendor/chart.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(s.webDir, "vendor", "chart.umd.js"))
	})

	// Initialize config.
	if err := config.InitConfig(s.webDir); err != nil {
		log.Printf("[View] Warn: config init: %v", err)
	}

	// Setup monitor event handler via controller.
	s.controller.GetMonitor().OnEvent(func(eventType string, data monitor.EventData) {
		if eventType == monitor.EventAnyGift {
			go s.controller.HandleGiftEvent(data)
		}
		if eventType == monitor.EventChatMessage {
			go s.controller.HandleChatMessageEvent(data)
		}
		if eventType == monitor.EventGiftUser {
			if id, err := s.controller.RecordTargetGiftReceived(data); err != nil {
				log.Printf("[View] Error recording target gift history: %v", err)
			} else {
				data["historyId"] = id
			}
		}
		s.broadcastSSE(eventType, data)
	})

	addr := fmt.Sprintf("%s:%d", host, port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(mux),
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
		s.httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("[View] Running at http://%s:%d", host, port)
	return s.httpServer.ListenAndServe()
}

// corsMiddleware adds CORS headers so the UI can run on another origin
// (ex.: hospedada na Vercel apontando para esta API). Defina CORS_ORIGIN
// para restringir a origem permitida (padrão: "*").
func corsMiddleware(next http.Handler) http.Handler {
	origin := os.Getenv("CORS_ORIGIN")
	if origin == "" {
		origin = "*"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			if origin == "*" || strings.Contains(origin, o) {
				w.Header().Set("Access-Control-Allow-Origin", o)
				w.Header().Set("Vary", "Origin")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- SSE Handlers ---

func (s *HTTPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// Send initial state.
	state := s.controller.GetState()
	initial := map[string]interface{}{
		"connected":    state.Connected,
		"username":     state.Username,
		"aiConfigured": true,
	}
	s.writeSSE(w, "server-state", initial)
	flusher.Flush()

	s.sseMu.Lock()
	s.sseClients[w] = true
	s.sseMu.Unlock()

	// Wait for client disconnect.
	<-r.Context().Done()

	s.sseMu.Lock()
	delete(s.sseClients, w)
	s.sseMu.Unlock()
}

func (s *HTTPServer) broadcastSSE(eventType string, data interface{}) {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()

	for client := range s.sseClients {
		s.writeSSE(client, eventType, data)
		if flusher, ok := client.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func (s *HTTPServer) writeSSE(w http.ResponseWriter, event string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

// --- API Handlers ---

func (s *HTTPServer) handleState(w http.ResponseWriter, r *http.Request) {
	state := s.controller.GetState()
	writeJSON(w, state)
}

func (s *HTTPServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, s.controller.GetSettings())
		return
	}
	if r.Method == http.MethodPost {
		var settings monitor.Settings
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.controller.SetSettings(settings)
		// Broadcast updated settings to all SSE clients
		s.broadcastSSE("settings-update", s.controller.GetSettings())
		writeJSON(w, map[string]bool{"success": true})
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *HTTPServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		idStr := r.URL.Path[len("/api/history/"):]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		deleted, err := s.controller.DeleteModeration(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "deleted": deleted})
		return
	}

	history, err := s.controller.GetRecentModerations(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, history)
}

func (s *HTTPServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	// Start LLM worker in background.
	go func() {
		ctx := context.Background()
		llmReady, err := s.controller.ProbeReady(ctx)
		if err != nil {
			log.Printf("[View] LLM worker warmup error: %v", err)
		}
		if err := s.controller.WarmupModeration(ctx, llmReady, false); err != nil {
			log.Printf("[View] Warmup warning: %v", err)
		}
	}()

	if err := s.controller.StartMonitoring(context.Background(), body.Username); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.controller.StopMonitoring()
	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.controller.ClearHistory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "deleted": deleted})
}

func (s *HTTPServer) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Comment  string `json:"comment"`
		Category string `json:"category"`
		Expected string `json:"expected"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	_, err := s.controller.AddFeedback(body.Comment, body.Category, body.Expected)
	if err != nil {
		statusCode := http.StatusInternalServerError
		switch err.Error() {
		case "comment is required", "invalid category", "invalid expected":
			statusCode = http.StatusBadRequest
		}
		writeError(w, statusCode, err.Error())
		return
	}

	// Clear cache and re-warmup.
	s.controller.ClearModerationCache()
	ctx := r.Context()
	llmReady, _ := s.controller.ProbeReady(ctx)
	s.controller.WarmupModeration(ctx, llmReady, true)

	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	llmReady, _ := s.controller.ProbeReady(ctx)
	modStatus := s.controller.GetStartupStatus()

	writeJSON(w, map[string]interface{}{
		"ready":        modStatus.Ready && llmReady,
		"llmReady":     llmReady,
		"moderation":   modStatus,
		"aiConfigured": true,
	})
}

func (s *HTTPServer) handleProbeLLM(w http.ResponseWriter, r *http.Request) {
	ready, err := s.controller.ProbeReady(r.Context())
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"llmActive": false,
			"error":     err.Error(),
		})
		return
	}
	writeJSON(w, map[string]interface{}{"llmActive": ready})
}

func (s *HTTPServer) handleWorkerRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	s.controller.RegisterWorker(body.Host, body.Port)
	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleGifts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		limit := 100
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		userID := r.URL.Query().Get("user")
		if userID != "" {
			gifts, err := s.controller.GetGiftsByUser(userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, gifts)
			return
		}
		state := s.controller.GetState()
		gifts, err := s.controller.GetRecentGifts(state.Username, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, gifts)
		return
	}
	if r.Method == http.MethodDelete {
		affected, err := s.controller.ClearGifts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "deleted": affected})
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *HTTPServer) handleAvailableGifts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	gifts, err := s.controller.FetchAvailableGifts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, gifts)
}

func (s *HTTPServer) handleTargetGiftHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.controller.GetRecentTargetGiftHistory(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []model.TargetGiftHistory{}
	}
	writeJSON(w, items)
}

func (s *HTTPServer) handleTargetGiftHistoryAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ID           int64  `json:"id"`
		ResponseType string `json:"responseType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if body.ResponseType != model.TargetGiftResponseManual && body.ResponseType != model.TargetGiftResponseAutomatic {
		writeError(w, http.StatusBadRequest, "responseType must be manual or automatic")
		return
	}
	if err := s.controller.AnswerTargetGift(body.ID, body.ResponseType); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleAskAI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Question == "" {
		writeError(w, http.StatusBadRequest, "question is required")
		return
	}

	ctx := r.Context()
	response, err := s.controller.AskAI(ctx, body.Question)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("AI error: %v", err))
		return
	}

	writeJSON(w, map[string]string{
		"question": body.Question,
		"answer":   response,
	})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[View] writeJSON error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
