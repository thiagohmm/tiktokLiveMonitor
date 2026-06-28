// Package server provides the HTTP server with SSE and REST API for the TikTok Live Monitor.
package server

import (
	"context"
	"encoding/json"
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

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/config"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/moderation"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// Server is the HTTP application server.
type Server struct {
	httpServer   *http.Server
	mon          *MonitorWrapper
	aiMgr        *ai.Manager
	modEngine    *moderation.Engine
	db           *database.DB
	sseClients   map[http.ResponseWriter]bool
	sseMu        sync.Mutex
	modelsDir    string
	binDir       string
	webDir       string
}

// MonitorWrapper adapts monitor.Monitor for server use.
type MonitorWrapper struct {
	m          *monitor.Monitor
	cancelFunc context.CancelFunc
	ctx        context.Context
}

// Config holds server configuration.
type Config struct {
	Host      string
	Port      int
	ModelsDir string
	BinDir    string
	WebDir    string
}

// New creates a new Server.
func New(cfg Config, db *database.DB, aiMgr *ai.Manager, modEngine *moderation.Engine, mon *monitor.Monitor) *Server {
	return &Server{
		mon: &MonitorWrapper{
			m:   mon,
			ctx: context.Background(),
		},
		aiMgr:      aiMgr,
		modEngine:  modEngine,
		db:         db,
		sseClients: make(map[http.ResponseWriter]bool),
		modelsDir:  cfg.ModelsDir,
		binDir:     cfg.BinDir,
		webDir:     cfg.WebDir,
	}
}

// Start begins listening and returns a channel that closes when the server is done.
func (s *Server) Start(ctx context.Context) error {
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

	// SSE.
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
	mux.HandleFunc("/api/ask-ai", s.handleAskAI)

	// Static files.
	mux.Handle("/", http.FileServer(http.Dir(s.webDir)))

	// Chart.js vendor.
	mux.HandleFunc("/vendor/chart.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(s.webDir, "vendor", "chart.umd.js"))
	})

	// Initialize config.
	if err := config.InitConfig(s.webDir); err != nil {
		log.Printf("[Server] Warn: config init: %v", err)
	}

	// Setup monitor event handler.
	s.mon.m.OnEvent(func(eventType string, data monitor.EventData) {
		if eventType == monitor.EventAnyGift {
			go func() {
				uniqueID := data["uniqueId"]
				nickname := data["nickname"]
				giftName := data["giftName"]
				repeatCount := 1
				if rc, ok := data["repeatCount"]; ok {
					if rcInt, ok := rc.(int); ok {
						repeatCount = rcInt
					}
				}
				giftType := 0
				if gt, ok := data["giftType"]; ok {
					if gtInt, ok := gt.(int); ok {
						giftType = gtInt
					}
				}
				state := s.mon.m.GetState()
				if _, err := s.db.AddGift(state.Username, uniqueID.(string), nickname.(string), giftName.(string), repeatCount, giftType); err != nil {
					log.Printf("[Server] Error storing gift: %v", err)
				}
			}()
		}
		if eventType == monitor.EventChatMessage {
			go func() {
				uniqueID := data["uniqueId"]
				nickname := data["nickname"]
				comment := data["comment"]
				if err := s.db.AddUserMessageDedup(uniqueID.(string), nickname.(string), comment.(string)); err != nil {
					log.Printf("[Server] Error storing user message: %v", err)
				}
			}()
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
		<-sigCh
		log.Println("[Server] Shutting down...")
		s.mon.m.StopMonitoring()
		s.aiMgr.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("[Server] Running at http://%s:%d", host, port)
	return s.httpServer.ListenAndServe()
}

// --- SSE ---

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
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
	state := s.mon.m.GetState()
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

func (s *Server) broadcastSSE(eventType string, data interface{}) {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()

	for client := range s.sseClients {
		s.writeSSE(client, eventType, data)
		if flusher, ok := client.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func (s *Server) writeSSE(w http.ResponseWriter, event string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

// --- API Handlers ---

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	state := s.mon.m.GetState()
	writeJSON(w, state)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, s.mon.m.GetSettings())
		return
	}
	if r.Method == http.MethodPost {
		var settings monitor.Settings
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mon.m.SetSettings(settings)
		writeJSON(w, map[string]bool{"success": true})
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		idStr := r.URL.Path[len("/api/history/"):]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		deleted, err := s.db.DeleteModeration(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "deleted": deleted})
		return
	}

	history, err := s.db.GetRecentModerations(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, history)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
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

	// Warmup moderation.
	ctx := context.Background()
	llmReady, _ := s.aiMgr.ProbeReady(ctx)
	if _, err := s.modEngine.WarmupLearning(ctx, llmReady, false); err != nil {
		log.Printf("[Server] Warmup warning: %v", err)
	}

	// Create a cancellable context for monitoring.
	monCtx, cancel := context.WithCancel(context.Background())
	s.mon.cancelFunc = cancel
	s.mon.ctx = monCtx

	if err := s.mon.m.StartMonitoring(monCtx, body.Username); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if s.mon.cancelFunc != nil {
		s.mon.cancelFunc()
	}
	s.mon.m.StopMonitoring()
	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.db.ClearHistory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "deleted": deleted})
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Comment  string `json:"comment"`
		Category string `json:"category"`
		Expected string `json:"expected"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	_, err := s.db.AddFeedback(body.Comment, body.Category, body.Expected)
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
	s.modEngine.ClearCache()
	ctx := r.Context()
	llmReady, _ := s.aiMgr.ProbeReady(ctx)
	s.modEngine.WarmupLearning(ctx, llmReady, true)

	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	llmReady, _ := s.aiMgr.ProbeReady(ctx)
	modStatus := s.modEngine.GetStartupStatus()

	writeJSON(w, map[string]interface{}{
		"ready":        modStatus.Ready && llmReady,
		"llmReady":     llmReady,
		"moderation":   modStatus,
		"aiConfigured": true,
	})
}

func (s *Server) handleProbeLLM(w http.ResponseWriter, r *http.Request) {
	ready, err := s.aiMgr.ProbeReady(r.Context())
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"llmActive": false,
			"error":     err.Error(),
		})
		return
	}
	writeJSON(w, map[string]interface{}{"llmActive": ready})
}

func (s *Server) handleWorkerRegister(w http.ResponseWriter, r *http.Request) {
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

	s.aiMgr.RegisterWorker(body.Host, body.Port)
	writeJSON(w, map[string]bool{"success": true})
}

func (s *Server) handleGifts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		limit := 100
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		userID := r.URL.Query().Get("user")
		if userID != "" {
			gifts, err := s.db.GetGiftsByUser(userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, gifts)
			return
		}
		gifts, err := s.db.GetRecentGifts(limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, gifts)
		return
	}
	if r.Method == http.MethodDelete {
		affected, err := s.db.ClearGifts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "deleted": affected})
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *Server) handleAskAI(w http.ResponseWriter, r *http.Request) {
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

	giftSummary, err := s.db.GetGiftSummary()
	if err != nil {
		log.Printf("[Server] Error fetching gift summary for AI: %v", err)
	}

	allMessages, err := s.db.GetAllUserMessages()
	if err != nil {
		log.Printf("[Server] Error fetching user messages for AI: %v", err)
	}

	var ctxBuilder strings.Builder

	if len(allMessages) > 0 {
		ctxBuilder.WriteString("MENSAGENS DOS USUÁRIOS NA LIVE (até 10 por usuário, sem repetidas):\n")
		for uid, msgs := range allMessages {
			ctxBuilder.WriteString(fmt.Sprintf("\n  %s:\n", uid))
			for _, m := range msgs {
				ctxBuilder.WriteString(fmt.Sprintf("    - %s\n", m.Message))
			}
		}
	} else {
		ctxBuilder.WriteString("NENHUMA MENSAGEM DE USUÁRIO REGISTRADA.\n")
	}

	ctxBuilder.WriteString("\n")

	if len(giftSummary) > 0 {
		ctxBuilder.WriteString("PRESENTES ENVIADOS NA LIVE:\n")
		for uid, giftsMap := range giftSummary {
			var parts []string
			for gname, count := range giftsMap {
				parts = append(parts, fmt.Sprintf("%s x%d", gname, count))
			}
			ctxBuilder.WriteString(fmt.Sprintf("- %s: %s\n", uid, strings.Join(parts, ", ")))
		}
	} else {
		ctxBuilder.WriteString("NENHUM PRESENTE REGISTRADO NA LIVE.\n")
	}

	systemPrompt := fmt.Sprintf(`Você é um assistente que responde perguntas sobre uma live do TikTok.
Use os dados abaixo para responder. Busque informações nos dados de mensagens e presentes.
Se a pergunta não estiver relacionada aos dados disponíveis, diga que não encontrou informações.

%s

Responda em português do Brasil de forma direta e concisa.`, ctxBuilder.String())

	ctx := r.Context()
	req := ai.CompletionRequest{
		SystemContent: systemPrompt,
		UserContent:   body.Question,
		MaxTokens:     512,
	}

	response, err := s.aiMgr.Complete(ctx, req)
	if err != nil {
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
		log.Printf("[Server] writeJSON error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
