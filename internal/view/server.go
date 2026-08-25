// Package view provides the HTTP server (View layer) with SSE and REST API for the TikTok Live Monitor.
package view

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/alerts"
	"github.com/thiagohmm/tiktok-live-monitor/internal/config"
	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// BuildVersion identifies the compiled build. It is injected at link time via
// -ldflags "-X github.com/thiagohmm/tiktok-live-monitor/internal/view.BuildVersion=..."
// and used to bust browser caches for static assets (e.g. renderer.js).
var BuildVersion = "dev"

// HTTPServer is the presentation layer (View) that handles HTTP requests.
type HTTPServer struct {
	httpServer *http.Server
	controller *controller.AppController
	sseClients map[http.ResponseWriter]bool
	sseMu      sync.Mutex
	webDir     string
	cfg        Config
}

// handleRoot serves the main UI (index.html) with a build-version query string
// injected into the renderer.js <script> tag to bust browser caches. Any other
// path is delegated to the static file server.
func (s *HTTPServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.FileServer(http.Dir(s.webDir)).ServeHTTP(w, r)
		return
	}

	indexHTML, err := os.ReadFile(filepath.Join(s.webDir, "index.html"))
	if err != nil {
		http.Error(w, "Página inicial não encontrada.", http.StatusInternalServerError)
		return
	}

	tmpl, err := template.New("index.html").Option("missingkey=error").Parse(string(indexHTML))
	if err != nil {
		http.Error(w, "Falha ao renderizar a página.", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, map[string]string{"BuildVersion": BuildVersion}); err != nil {
		log.Printf("[View] Error rendering index.html: %v", err)
	}
}

// Config holds server configuration.
type Config struct {
	Host       string
	Port       int
	ModelsDir  string
	BinDir     string
	WebDir     string
	AgentProxy http.Handler
	// AgentBaseURL is the base URL of the Python agent HTTP API, used by the
	// transient compatibility layer that forwards /api/ask-ai, /api/probe-llm
	// and /api/feedback to the agent (docs/plano-unificacao-ia.md, fase 2).
	AgentBaseURL string
}

// New creates a new HTTP server (View).
func New(cfg Config, ctrl *controller.AppController) *HTTPServer {
	return &HTTPServer{
		controller: ctrl,
		sseClients: make(map[http.ResponseWriter]bool),
		webDir:     cfg.WebDir,
		cfg:        cfg,
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
	mux.HandleFunc("/api/ask-ai", s.handleAskAI)
	mux.HandleFunc("/api/gifts", s.handleGifts)
	mux.HandleFunc("/api/available-gifts", s.handleAvailableGifts)
	mux.HandleFunc("/api/target-gift-history", s.handleTargetGiftHistory)
	mux.HandleFunc("/api/target-gift-history/answer", s.handleTargetGiftHistoryAnswer)
	mux.HandleFunc("/api/pinned-comments", s.handlePinnedComments)
	mux.HandleFunc("/api/ranking", s.handleRanking)
	mux.HandleFunc("/api/report", s.handleReport)
	mux.HandleFunc("/api/profile", s.handleProfile)
	mux.HandleFunc("/api/alert-config", s.handleAlertConfig)
	mux.HandleFunc("/api/admin/lives", s.handleAdminLives)

	// Agent proxy: forward /agent/* to the Python agent HTTP API.
	if s.cfg.AgentProxy != nil {
		mux.Handle("/agent/", s.cfg.AgentProxy)
	}

	// Root page: render index.html with a cache-busting build version so the
	// browser always re-fetches renderer.js after a rebuild. Every other path
	// is delegated to the static file server.
	mux.HandleFunc("/", s.handleRoot)

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
		if eventType == monitor.EventNewSocialEvent {
			go s.controller.HandleShareEvent(data)
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

	addr := fmt.Sprintf("%s:%d", host, port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
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

	// Warmup moderation in background.
	go func() {
		ctx := context.Background()
		if err := s.controller.WarmupModeration(ctx, false); err != nil {
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
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Compat layer (fase 2): forward to the Python agent, which now owns
	// feedback.db. On success, refresh the moderation allowlist so identical
	// messages stop being flagged immediately.
	status, body, err := s.forwardToAgent(r, "/feedback")
	if err != nil {
		writeError(w, http.StatusBadGateway, "agent unavailable")
		return
	}
	if status == http.StatusOK {
		s.controller.ClearModerationCache()
		s.controller.WarmupModeration(r.Context(), true)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func (s *HTTPServer) handleProbeLLM(w http.ResponseWriter, r *http.Request) {
	status, body, err := s.forwardToAgent(r, "/probe-llm")
	if err != nil {
		// Fallback determinístico: sem agente, IA inativa.
		writeJSON(w, map[string]interface{}{"llmActive": false})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func (s *HTTPServer) handleAskAI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status, body, err := s.forwardToAgent(r, "/ask-ai")
	if err != nil {
		writeError(w, http.StatusBadGateway, "agent unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

// forwardToAgent relays the request to the Python agent HTTP API, preserving
// method and body. Returns the response status and raw body.
func (s *HTTPServer) forwardToAgent(r *http.Request, path string) (int, []byte, error) {
	if s.cfg.AgentBaseURL == "" {
		return 0, nil, fmt.Errorf("agent base URL not configured")
	}
	target := strings.TrimRight(s.cfg.AgentBaseURL, "/") + path
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

func (s *HTTPServer) handleReadiness(w http.ResponseWriter, r *http.Request) {
	modStatus := s.controller.GetStartupStatus()

	writeJSON(w, map[string]interface{}{
		"ready":      modStatus.Ready,
		"moderation": modStatus,
	})
}

func (s *HTTPServer) handleGifts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		limit := 200
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
			if gifts == nil {
				gifts = []model.Gift{}
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
		if gifts == nil {
			gifts = []model.Gift{}
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
		log.Printf("[View] available-gifts: %v", err)
		gifts = []string{}
	}
	if gifts == nil {
		gifts = []string{}
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
	pending := r.URL.Query().Get("pending")
	var (
		items []model.TargetGiftHistory
		err   error
	)
	if pending == "1" || strings.EqualFold(pending, "true") {
		items, err = s.controller.GetPendingTargetGiftHistory(limit)
	} else {
		items, err = s.controller.GetRecentTargetGiftHistory(limit)
	}
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

func (s *HTTPServer) handlePinnedComments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 15
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.controller.GetRecentPinnedComments(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []model.PinnedComment{}
	}
	writeJSON(w, items)
}

// handleRanking returns the engagement ranking for a live.
func (s *HTTPServer) handleRanking(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := r.URL.Query()
	liveName := query.Get("live")
	if liveName == "" {
		state := s.controller.GetState()
		liveName = state.Username
	}
	ranking, err := s.controller.GetLiveRanking(liveName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, ranking)
}

// handleAdminLives returns derived lives and schedules stored in the database.
func (s *HTTPServer) handleAdminLives(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	lives, err := s.controller.GetLives(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"lives": lives})
}

// handleReport generates an AI-assisted post-live report.
func (s *HTTPServer) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := r.URL.Query()
	liveName := query.Get("live")
	if liveName == "" {
		state := s.controller.GetState()
		liveName = state.Username
	}
	report, err := s.controller.GenerateReport(r.Context(), liveName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, report)
}

// handleProfile returns the historical profile for a participant.
func (s *HTTPServer) handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uniqueID := r.URL.Query().Get("uid")
	if uniqueID == "" {
		writeError(w, http.StatusBadRequest, "uid is required")
		return
	}
	profile, err := s.controller.GetUserProfile(uniqueID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, profile)
}

// handleAlertConfig gets or updates the alert webhook configuration.
func (s *HTTPServer) handleAlertConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.controller.GetAlertConfig())
	case http.MethodPost:
		var body alerts.Config
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		s.controller.SetAlertConfig(body)
		writeJSON(w, map[string]string{"success": "ok"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
