// Package view provides the HTTP server (View layer) with SSE and REST API for the TikTok Live Monitor.
//
// O backend é uma API pura (SSE + REST): a UI estática vive em /frontend e é
// servida separadamente (nginx, Vercel ou dev server local), que faz proxy
// para /api/* e /events.
package view

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
)

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
			writeInternalError(w, r, err)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "deleted": deleted})
		return
	}

	history, err := s.controller.GetRecentModerations(100)
	if err != nil {
		writeInternalError(w, r, err)
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

	if err := s.controller.StartMonitoring(context.Background(), body.Username); err != nil {
		writeInternalError(w, r, err)
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
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "deleted": deleted})
}

func (s *HTTPServer) handleReadiness(w http.ResponseWriter, r *http.Request) {
	s.sseMu.Lock()
	sse := len(s.sseClients)
	s.sseMu.Unlock()
	writeJSON(w, map[string]interface{}{
		"ready":      true,
		"sseClients": sse,
		"goroutines": runtime.NumGoroutine(),
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
				writeInternalError(w, r, err)
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
			writeInternalError(w, r, err)
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
			writeInternalError(w, r, err)
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
		writeInternalError(w, r, err)
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
		writeInternalError(w, r, err)
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
		writeInternalError(w, r, err)
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
	ranking, err := s.controller.GetLiveRanking(liveName, query.Get("mode"))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, ranking)
}

// handleAdminLives returns derived lives and schedules stored in the database.
func (s *HTTPServer) handleAdminLives(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.RequireAdmin(w, r, s.auth); !ok {
		return
	}
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
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{"lives": lives})
}

// handleAdminLivesDelete removes all stored data for a live.
func (s *HTTPServer) handleAdminLivesDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.RequireAdmin(w, r, s.auth); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	liveName := strings.TrimSpace(r.URL.Query().Get("live"))
	if liveName == "" {
		writeError(w, http.StatusBadRequest, "live is required")
		return
	}
	deleted, err := s.controller.DeleteLive(liveName)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{"deleted": deleted})
}

// handleReport generates a deterministic post-live report.
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
		writeInternalError(w, r, err)
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
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, profile)
}

// handleGoals returns the current live's goals (GET) or creates/updates a goal (POST).
func (s *HTTPServer) handleGoals(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		state, err := s.controller.GetGoalsState()
		if err != nil {
			writeInternalError(w, r, err)
			return
		}
		writeJSON(w, state)
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			ID          int64                 `json:"id"`
			Title       string                `json:"title"`
			GiftName    string                `json:"giftName"`
			TargetUnits int                   `json:"targetUnits"`
			Milestones  []model.GoalMilestone `json:"milestones"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if strings.TrimSpace(body.Title) == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		if body.TargetUnits < 1 {
			writeError(w, http.StatusBadRequest, "targetUnits must be >= 1")
			return
		}

		if body.ID > 0 {
			// Update: preserve status/milestones timestamps of the existing goal.
			state, err := s.controller.GetGoalsState()
			if err != nil {
				writeInternalError(w, r, err)
				return
			}
			existing := findGoal(state, body.ID)
			if existing == nil {
				writeError(w, http.StatusNotFound, "goal not found")
				return
			}
			existing.Title = body.Title
			existing.GiftName = body.GiftName
			existing.TargetUnits = body.TargetUnits
			if body.Milestones != nil {
				existing.Milestones = body.Milestones
			}
			if err := s.controller.UpdateGoal(*existing); err != nil {
				writeInternalError(w, r, err)
				return
			}
			writeJSON(w, *existing)
			return
		}

		goal, err := s.controller.CreateGoal(body.Title, body.GiftName, body.TargetUnits, body.Milestones)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, goal)
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *HTTPServer) handleGoalCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := goalIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.controller.CancelGoal(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleGoalComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := goalIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.controller.CompleteGoal(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

// goalIDParam reads the required ?id= query parameter of the goal
// cancel/complete endpoints (a live can have several active goals).
func goalIDParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("id é obrigatório")
	}
	return id, nil
}

// findGoal locates a goal by ID in the live's goal state.
func findGoal(state controller.GoalsState, id int64) *model.GiftGoal {
	for i := range state.Actives {
		if state.Actives[i].Goal.ID == id {
			return &state.Actives[i].Goal
		}
	}
	for i := range state.History {
		if state.History[i].ID == id {
			return &state.History[i]
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[View] writeJSON error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		log.Printf("[View] writeError: %v", err)
	}
}

// writeInternalError responde com mensagem genérica (evita vazar detalhes
// do banco/SQL ao cliente) e registra o erro real no log do servidor.
func writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("[View] erro interno (%s %s): %v", r.Method, r.URL.Path, err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "erro interno do servidor"})
}

// maxRequestBodyBytes limita o corpo das requisições (payloads JSON desta API
// são pequenos): impede esgotamento de memória com corpos gigantes.
const maxRequestBodyBytes = 1 << 20

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders adiciona headers defensivos básicos a todas as respostas.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
