package view

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// Sorteio ao vivo: coleta por palavra-chave + entradas manuais e sorteio de N
// vencedores. O estado vive no Monitor da live (sem persistência nesta
// versão); a palavra-chave persiste nas settings da organização. Mutações
// publicam os mesmos eventos que a coleta emite (sorteio-state/result), no
// padrão settings-update.

// sorteioMaxWinners bounds the winners per draw request.
const sorteioMaxWinners = 50

// sorteioStateView is the GET /api/sorteio payload: state + resolved live +
// the org keyword used for chat collection.
type sorteioStateView struct {
	Active       bool                         `json:"active"`
	Live         string                       `json:"live"`
	Keyword      string                       `json:"keyword"`
	Participants []monitor.SorteioParticipant `json:"participants"`
	Winners      []monitor.SorteioParticipant `json:"winners"`
}

func (s *HTTPServer) sorteioView(orgID string, state monitor.SorteioState, resolved string) sorteioStateView {
	keyword := s.controller.GetSettings(orgID).SorteioKeyword
	if state.Participants == nil {
		state.Participants = []monitor.SorteioParticipant{}
	}
	if state.Winners == nil {
		state.Winners = []monitor.SorteioParticipant{}
	}
	return sorteioStateView{
		Active:       state.Active,
		Live:         resolved,
		Keyword:      keyword,
		Participants: state.Participants,
		Winners:      state.Winners,
	}
}

// writeSorteioError maps known raffle errors to user-facing status codes.
func writeSorteioError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, controller.ErrSorteioNoLive):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, monitor.ErrSorteioInsufficientPool),
		errors.Is(err, monitor.ErrSorteioEmptyName):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, monitor.ErrSorteioFull),
		errors.Is(err, monitor.ErrLiveNotMonitored):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, model.ErrOrgRequired):
		writeError(w, http.StatusBadRequest, "organização obrigatória")
	default:
		writeInternalError(w, r, err)
	}
}

// sorteioBody decodes an optional JSON body: empty or chunked-empty bodies
// leave dst untouched (validation happens on the typed fields).
func sorteioBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	err := dec.Decode(dst)
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body")
		return false
	}
	return true
}

// handleSorteioState returns the live raffle state (GET /api/sorteio).
func (s *HTTPServer) handleSorteioState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	state, resolved, err := s.controller.SorteioState(t.OrgID, r.URL.Query().Get("live"))
	if err != nil {
		writeSorteioError(w, r, err)
		return
	}
	writeJSON(w, s.sorteioView(t.OrgID, state, resolved))
}

// handleSorteioStart starts chat keyword collection (POST /api/sorteio/start).
// The body's keyword is optional: when present it updates the org settings —
// after the live is validated, so a missing live never persists settings.
func (s *HTTPServer) handleSorteioStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	liveParam := r.URL.Query().Get("live")
	if _, _, err := s.controller.SorteioState(t.OrgID, liveParam); err != nil {
		writeSorteioError(w, r, err)
		return
	}
	var body struct {
		Keyword string `json:"keyword"`
	}
	if !sorteioBody(w, r, &body) {
		return
	}
	if keyword := strings.TrimSpace(body.Keyword); keyword != "" {
		if runes := len([]rune(keyword)); runes > 40 {
			writeError(w, http.StatusBadRequest, "A palavra-chave deve ter no máximo 40 caracteres.")
			return
		}
		settings := s.controller.GetSettings(t.OrgID)
		settings.SorteioKeyword = keyword
		if err := s.controller.SetSettings(t.OrgID, settings); err != nil {
			writeInternalError(w, r, err)
			return
		}
		s.publishSSE(t.OrgID, "settings-update", s.controller.GetSettings(t.OrgID))
	}
	state, resolved, err := s.controller.StartSorteio(t.OrgID, liveParam)
	if err != nil {
		writeSorteioError(w, r, err)
		return
	}
	writeJSON(w, s.sorteioView(t.OrgID, state, resolved))
}

// handleSorteioStop stops chat keyword collection, keeping the participants.
func (s *HTTPServer) handleSorteioStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	state, resolved, err := s.controller.StopSorteio(t.OrgID, r.URL.Query().Get("live"))
	if err != nil {
		writeSorteioError(w, r, err)
		return
	}
	writeJSON(w, s.sorteioView(t.OrgID, state, resolved))
}

// handleSorteioReset clears participants and the last result.
func (s *HTTPServer) handleSorteioReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	state, resolved, err := s.controller.ResetSorteio(t.OrgID, r.URL.Query().Get("live"))
	if err != nil {
		writeSorteioError(w, r, err)
		return
	}
	writeJSON(w, s.sorteioView(t.OrgID, state, resolved))
}

// handleSorteioDraw draws count winners (POST /api/sorteio/draw).
func (s *HTTPServer) handleSorteioDraw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Count int `json:"count"`
	}
	if !sorteioBody(w, r, &body) {
		return
	}
	if body.Count < 1 || body.Count > sorteioMaxWinners {
		writeError(w, http.StatusBadRequest, "Informe entre 1 e 50 vencedores.")
		return
	}
	winners, resolved, err := s.controller.DrawSorteio(t.OrgID, r.URL.Query().Get("live"), body.Count)
	if err != nil {
		writeSorteioError(w, r, err)
		return
	}
	if winners == nil {
		winners = []monitor.SorteioParticipant{}
	}
	state, _, err := s.controller.SorteioState(t.OrgID, resolved)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{
		"live":    resolved,
		"winners": winners,
		"state":   s.sorteioView(t.OrgID, state, resolved),
	})
}

// handleSorteioAddParticipant adds an operator-typed participant
// (POST /api/sorteio/participants).
func (s *HTTPServer) handleSorteioAddParticipant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Nickname string `json:"nickname"`
	}
	if !sorteioBody(w, r, &body) {
		return
	}
	p, resolved, added, err := s.controller.AddSorteioManualParticipant(t.OrgID, r.URL.Query().Get("live"), body.Nickname)
	if err != nil {
		writeSorteioError(w, r, err)
		return
	}
	state, _, err := s.controller.SorteioState(t.OrgID, resolved)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{
		"added":       added,
		"participant": p,
		"state":       s.sorteioView(t.OrgID, state, resolved),
	})
}

// handleSorteioRemoveParticipant removes one participant by id/name
// (POST /api/sorteio/participants/remove).
func (s *HTTPServer) handleSorteioRemoveParticipant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		UniqueID string `json:"uniqueId"`
	}
	if !sorteioBody(w, r, &body) {
		return
	}
	removed, resolved, err := s.controller.RemoveSorteioParticipant(t.OrgID, r.URL.Query().Get("live"), body.UniqueID)
	if err != nil {
		writeSorteioError(w, r, err)
		return
	}
	state, _, err := s.controller.SorteioState(t.OrgID, resolved)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]interface{}{
		"removed": removed,
		"state":   s.sorteioView(t.OrgID, state, resolved),
	})
}
