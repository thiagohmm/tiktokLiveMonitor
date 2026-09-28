// Package view provides the HTTP server (View layer) with SSE and REST API for the TikTok Live Monitor.
//
// O backend é uma API pura (SSE + REST): a UI estática vive em /frontend e é
// servida separadamente (nginx, Vercel ou dev server local), que faz proxy
// para /api/* e /events.
package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	writeJSON(w, s.controller.GetState(t.OrgID))
}

func (s *HTTPServer) handleLives(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	writeJSON(w, map[string]interface{}{"lives": s.controller.GetLiveStates(t.OrgID)})
}

func (s *HTTPServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, s.controller.GetSettings(t.OrgID))
		return
	}
	if r.Method == http.MethodPost {
		var settings monitor.Settings
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		for _, quantity := range settings.TargetGiftQuantities {
			if quantity < 1 {
				writeError(w, http.StatusBadRequest, "A quantidade do presente alvo deve ser um inteiro maior ou igual a 1.")
				return
			}
		}
		for target, tag := range settings.TargetGiftTags {
			trimmed := strings.TrimSpace(tag)
			if trimmed == "" {
				delete(settings.TargetGiftTags, target)
				continue
			}
			if len([]rune(trimmed)) > 40 {
				writeError(w, http.StatusBadRequest, "A tag do presente alvo deve ter no máximo 40 caracteres.")
				return
			}
			settings.TargetGiftTags[target] = trimmed
		}
		if len(settings.TargetGifts) > maxTargetGifts {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("No máximo %d presentes alvo.", maxTargetGifts))
			return
		}
		if err := s.controller.SetSettings(t.OrgID, settings); err != nil {
			writeInternalError(w, r, err)
			return
		}
		// Only the organization's clients see its settings.
		s.publishSSE(t.OrgID, "settings-update", s.controller.GetSettings(t.OrgID))
		writeJSON(w, map[string]bool{"success": true})
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// maxTargetGifts bounds the target gift list of one organization.
const maxTargetGifts = 200

// maxListLimit caps the ?limit= of list endpoints.
const maxListLimit = 500

// limitParam reads ?limit= within [1, maxListLimit], falling back to def.
func limitParam(r *http.Request, def int) int {
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			if n > maxListLimit {
				return maxListLimit
			}
			return n
		}
	}
	return def
}

func (s *HTTPServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		idStr := strings.TrimPrefix(r.URL.Path, "/api/history/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		deleted, err := s.controller.DeleteModeration(t.OrgID, id)
		if err != nil {
			writeInternalError(w, r, err)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "deleted": deleted})
		return
	}

	history, err := s.controller.GetRecentModerations(t.OrgID, 100)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, history)
}

// writeMonitorError maps monitor start failures to client-facing statuses.
func writeMonitorError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, monitor.ErrOrgLiveLimit):
		writeError(w, http.StatusConflict, "Limite de lives simultâneas da organização atingido.")
	case errors.Is(err, model.ErrOrgRequired):
		writeError(w, http.StatusForbidden, msgNoOrganization)
	default:
		writeInternalError(w, r, err)
	}
}

func (s *HTTPServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}

	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(body.Username) == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	if err := s.controller.AttachMonitoring(context.Background(), t.OrgID, t.UserID, body.Username); err != nil {
		writeMonitorError(w, r, err)
		return
	}

	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	s.controller.DetachMonitoring(t.OrgID, t.UserID, r.URL.Query().Get("username"))
	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	// Destrutivo: só o dono da organização zera o histórico de moderação dela.
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	deleted, err := s.controller.ClearHistory(t.OrgID)
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
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		limit := limitParam(r, 200)
		userID := r.URL.Query().Get("user")
		if userID != "" {
			gifts, err := s.controller.GetGiftsByUser(t.OrgID, userID)
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
		live := s.controller.ResolveLive(t.OrgID, r.URL.Query().Get("live"))
		gifts, err := s.controller.GetRecentGifts(t.OrgID, live, limit)
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
		// Destrutivo: só o dono da organização apaga os presentes dela.
		if !t.CanManageOrg() {
			writeError(w, http.StatusForbidden, msgOrgOwnerOnly)
			return
		}
		affected, err := s.controller.ClearGifts(t.OrgID)
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
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	gifts, err := s.controller.FetchAvailableGifts(t.OrgID, r.URL.Query().Get("live"))
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
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	limit := limitParam(r, 50)
	live := r.URL.Query().Get("live")
	pending := r.URL.Query().Get("pending")
	var (
		items []model.TargetGiftHistory
		err   error
	)
	if pending == "1" || strings.EqualFold(pending, "true") {
		items, err = s.controller.GetPendingTargetGiftHistory(t.OrgID, live, limit)
	} else {
		items, err = s.controller.GetRecentTargetGiftHistory(t.OrgID, live, limit)
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
	t, ok := requestTenant(w, r)
	if !ok {
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
	if err := s.controller.AnswerTargetGift(t.OrgID, body.ID, body.ResponseType); err != nil {
		if errors.Is(err, model.ErrInvalidID) {
			writeError(w, http.StatusNotFound, "target gift not found")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleTargetGiftHistoryPriority(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		ID       int64 `json:"id"`
		Priority *bool `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if body.Priority == nil {
		writeError(w, http.StatusBadRequest, "priority is required")
		return
	}
	priorityAt, err := s.controller.SetTargetGiftPriority(t.OrgID, body.ID, *body.Priority)
	if err != nil {
		if errors.Is(err, model.ErrInvalidID) {
			writeError(w, http.StatusNotFound, "target gift not found or already answered")
			return
		}
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{
		"success": true, "id": body.ID, "isPriority": *body.Priority, "priorityAt": priorityAt,
	})
}

func (s *HTTPServer) handlePinnedComments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	items, err := s.controller.GetRecentPinnedComments(t.OrgID, r.URL.Query().Get("live"), limitParam(r, 15))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if items == nil {
		items = []model.PinnedComment{}
	}
	writeJSON(w, items)
}

// handleRanking returns the engagement ranking for one of the organization's lives.
func (s *HTTPServer) handleRanking(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	liveName := s.controller.ResolveLive(t.OrgID, query.Get("live"))
	ranking, err := s.controller.GetLiveRanking(t.OrgID, liveName, query.Get("mode"))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, ranking)
}

// handleAdminLives returns the live sessions stored for the organization.
func (s *HTTPServer) handleAdminLives(w http.ResponseWriter, r *http.Request) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	lives, err := s.controller.GetLives(t.OrgID, limitParam(r, 100))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{"lives": lives})
}

// handleAdminLivesSessionDelete removes all stored data of exactly one live
// session (id) of the organization. live and day are required as a guard: with
// a wrong id in hand the delete cannot silently hit another live. A session of
// another organization answers 404.
func (s *HTTPServer) handleAdminLivesSessionDelete(w http.ResponseWriter, r *http.Request) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := r.URL.Query()
	id := strings.TrimSpace(query.Get("id"))
	liveName := strings.TrimSpace(query.Get("live"))
	day := strings.TrimSpace(query.Get("day"))
	if id == "" || liveName == "" || day == "" {
		writeError(w, http.StatusBadRequest, "id, live e day são obrigatórios")
		return
	}

	session, err := s.controller.GetLiveSession(t.OrgID, id)
	switch {
	case errors.Is(err, model.ErrLiveSessionNotFound):
		writeError(w, http.StatusNotFound, "live não encontrada")
		return
	case err != nil:
		writeInternalError(w, r, err)
		return
	}
	if !strings.EqualFold(session.LiveName, liveName) || session.Day != day {
		writeError(w, http.StatusConflict, "id não corresponde à live/dia informados")
		return
	}

	deleted, err := s.controller.DeleteLive(t.OrgID, id)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{"deleted": deleted, "id": id})
}

// handleAdminLivesDelete is retired. The path is kept only to fail safely:
// a client still on the old contract (which sent just ?live=) must never reach
// a handler that deletes by streamer name. An old backend receiving the new
// path answers 404, so both rollout directions are safe.
func (s *HTTPServer) handleAdminLivesDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOrgManager(w, r); !ok {
		return
	}
	writeError(w, http.StatusGone, "endpoint removido: use POST /api/admin/lives/session/delete?id=&live=&day=")
}

// handleReport generates a deterministic post-live report.
func (s *HTTPServer) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	liveName := s.controller.ResolveLive(t.OrgID, r.URL.Query().Get("live"))
	report, err := s.controller.GenerateReport(r.Context(), t.OrgID, liveName)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, report)
}

// handleProfile returns the historical profile of a participant within the
// organization's lives.
func (s *HTTPServer) handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	uniqueID := r.URL.Query().Get("uid")
	if uniqueID == "" {
		writeError(w, http.StatusBadRequest, "uid is required")
		return
	}
	profile, err := s.controller.GetUserProfile(t.OrgID, uniqueID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, profile)
}

// handleGoals returns the goals of one of the organization's lives (GET) or
// creates/updates a goal (POST).
func (s *HTTPServer) handleGoals(w http.ResponseWriter, r *http.Request) {
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	live := r.URL.Query().Get("live")
	if r.Method == http.MethodGet {
		state, err := s.controller.GetGoalsState(t.OrgID, live)
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
			// Only goals of the organization's live are found here.
			state, err := s.controller.GetGoalsState(t.OrgID, live)
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

		goal, err := s.controller.CreateGoal(t.OrgID, live, body.Title, body.GiftName, body.TargetUnits, body.Milestones)
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
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	id, err := goalIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.controller.CancelGoal(t.OrgID, r.URL.Query().Get("live"), id); err != nil {
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
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	id, err := goalIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.controller.CompleteGoal(t.OrgID, r.URL.Query().Get("live"), id); err != nil {
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
