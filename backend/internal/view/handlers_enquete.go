package view

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// Enquete ao vivo: o operador define palavras-alvo (mínimo 2) e cada
// comentário do chat que for exatamente uma delas vale um voto. O voto é
// travado no primeiro comentário do usuário. O estado vive no Monitor da live
// (sem persistência, como o Sorteio), então é isolado por (org, live) e
// publicado apenas para a org dona.

// enqueteStateView is the payload of every poll endpoint: state + resolved live.
type enqueteStateView struct {
	Active    bool                    `json:"active"`
	Live      string                  `json:"live"`
	Question  string                  `json:"question"`
	Options   []monitor.EnqueteOption `json:"options"`
	Total     int                     `json:"total"`
	Winner    string                  `json:"winner"`
	Tied      bool                    `json:"tied"`
	StartedAt int64                   `json:"startedAt"`
	EndedAt   int64                   `json:"endedAt"`
}

func (s *HTTPServer) enqueteView(state monitor.EnqueteState, resolved string) enqueteStateView {
	if state.Options == nil {
		state.Options = []monitor.EnqueteOption{}
	}
	return enqueteStateView{
		Active:    state.Active,
		Live:      resolved,
		Question:  state.Question,
		Options:   state.Options,
		Total:     state.Total,
		Winner:    state.Winner,
		Tied:      state.Tied,
		StartedAt: state.StartedAt,
		EndedAt:   state.EndedAt,
	}
}

// writeEnqueteError maps known poll errors to user-facing status codes.
func writeEnqueteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, controller.ErrEnqueteNoLive),
		errors.Is(err, monitor.ErrEnqueteActive),
		errors.Is(err, monitor.ErrLiveNotMonitored):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, monitor.ErrEnqueteTooFewOptions),
		errors.Is(err, monitor.ErrEnqueteTooManyOptions),
		errors.Is(err, monitor.ErrEnqueteEmptyOption),
		errors.Is(err, monitor.ErrEnqueteMultiWordOption),
		errors.Is(err, monitor.ErrEnqueteLongOption),
		errors.Is(err, monitor.ErrEnqueteDuplicateOption),
		errors.Is(err, monitor.ErrEnqueteLongQuestion),
		errors.Is(err, model.ErrOrgRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeInternalError(w, r, err)
	}
}

// optionalJSONBody decodes an optional JSON body: empty or chunked-empty bodies
// leave dst untouched (validation happens on the typed fields).
func optionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	err := dec.Decode(dst)
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body")
		return false
	}
	return true
}

// handleEnqueteState returns the live poll state (GET /api/enquete).
func (s *HTTPServer) handleEnqueteState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	state, resolved, err := s.controller.EnqueteState(t.OrgID, r.URL.Query().Get("live"))
	if err != nil {
		writeEnqueteError(w, r, err)
		return
	}
	writeJSON(w, s.enqueteView(state, resolved))
}

// handleEnqueteStart opens a poll with the given words (POST /api/enquete/start).
func (s *HTTPServer) handleEnqueteStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Question string   `json:"question"`
		Words    []string `json:"words"`
	}
	if !optionalJSONBody(w, r, &body) {
		return
	}
	state, resolved, err := s.controller.StartEnquete(t.OrgID, r.URL.Query().Get("live"), body.Question, body.Words)
	if err != nil {
		writeEnqueteError(w, r, err)
		return
	}
	// O próprio monitor já publica EventEnqueteState (orgId no emit).
	writeJSON(w, s.enqueteView(state, resolved))
}

// handleEnqueteStop closes voting, keeping the votes for the final result.
func (s *HTTPServer) handleEnqueteStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	state, resolved, err := s.controller.StopEnquete(t.OrgID, r.URL.Query().Get("live"))
	if err != nil {
		writeEnqueteError(w, r, err)
		return
	}
	writeJSON(w, s.enqueteView(state, resolved))
}

// handleEnqueteReset clears options, votes and the final result.
func (s *HTTPServer) handleEnqueteReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	state, resolved, err := s.controller.ResetEnquete(t.OrgID, r.URL.Query().Get("live"))
	if err != nil {
		writeEnqueteError(w, r, err)
		return
	}
	// O próprio monitor já publica EventEnqueteState (orgId no emit).
	writeJSON(w, s.enqueteView(state, resolved))
}
