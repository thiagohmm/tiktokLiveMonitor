package view

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/teams"
)

func (s *HTTPServer) registerTeamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/org/invitations", s.handleInvitations)
	mux.HandleFunc("/api/org/invitations/", s.handleInvitationChange)
	mux.HandleFunc("/api/auth/invitations/accept", s.handleInvitationAccept)
	mux.HandleFunc("/api/org/seats", s.handleSeats)
	mux.HandleFunc("/api/org/allowed-lives", s.handleAllowedLives)
	mux.HandleFunc("/api/admin/orgs/", s.handleTeamAdministration)
	mux.HandleFunc("/api/admin/billing/price", s.handleSeatPrice)
}

func writeTeamError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, teams.ErrNotFound):
		writeError(w, 404, err.Error())
	case errors.Is(err, teams.ErrSuspended):
		writeError(w, 403, err.Error())
	case errors.Is(err, teams.ErrCapacity) || errors.Is(err, teams.ErrRegularization) || errors.Is(err, teams.ErrLastOwner):
		writeError(w, 409, err.Error())
	default:
		writeError(w, 400, "operação não concluída: "+safeTeamError(err))
	}
}
func safeTeamError(err error) string {
	if strings.Contains(err.Error(), "SQLSTATE") || strings.Contains(err.Error(), "sql:") {
		return "dados inválidos ou serviço indisponível"
	}
	return err.Error()
}

func (s *HTTPServer) handleInvitations(w http.ResponseWriter, r *http.Request) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		i, err := s.teams.Invitations(r.Context(), t.OrgID)
		if err != nil {
			writeTeamError(w, r, err)
			return
		}
		writeJSON(w, map[string]any{"invitations": i})
	case http.MethodPost:
		if s.auth.SiteURL == "" || s.mailer == nil || !s.mailer.Enabled() {
			writeError(w, 503, "configure SITE_URL e o envio de e-mail antes de convidar")
			return
		}
		key := "invite:" + t.OrgID
		ip := auth.ClientIP(r, s.proxyTrust)
		if s.lockout.RecordFailure(key, ip).Locked {
			writeError(w, 429, "aguarde antes de enviar novos convites")
			return
		}
		var body struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "invalid body")
			return
		}
		i, token, err := s.teams.CreateInvitation(r.Context(), t.OrgID, t.UserID, body.Email)
		if err != nil {
			writeTeamError(w, r, err)
			return
		}
		s.deliverInvitation(r, t.OrgID, i, token)
		writeJSON(w, map[string]any{"invitation": i, "message": "Confira a situação de envio na lista de convites."})
	default:
		writeError(w, 405, "method not allowed")
	}
}
func (s *HTTPServer) deliverInvitation(r *http.Request, org string, i teams.Invitation, token string) {
	name := org
	if o, err := s.controller.Repository().GetOrganization(org); err == nil {
		name = o.Name
	}
	err := s.mailer.SendInvitation(i.Email, name, s.auth.SiteURL+"/invite.html#token="+token)
	if markErr := s.teams.MarkDelivery(r.Context(), org, i.ID, token, err == nil); markErr != nil {
		log.Printf("[teams] delivery status: %v", markErr)
	}
}
func (s *HTTPServer) handleInvitationChange(w http.ResponseWriter, r *http.Request) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/org/invitations/"), "/")
	resend := len(parts) == 2 && parts[1] == "resend" && r.Method == http.MethodPost
	if !resend && (len(parts) != 1 || r.Method != http.MethodDelete) {
		writeError(w, 405, "method not allowed")
		return
	}
	if resend && (s.mailer == nil || !s.mailer.Enabled() || s.auth.SiteURL == "") {
		writeError(w, 503, "envio de e-mail indisponível")
		return
	}
	if resend && s.lockout.RecordFailure("invite:"+t.OrgID, auth.ClientIP(r, s.proxyTrust)).Locked {
		writeError(w, 429, "aguarde antes de reenviar")
		return
	}
	i, token, err := s.teams.ChangeInvitation(r.Context(), t.OrgID, t.UserID, parts[0], resend)
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	if resend {
		s.deliverInvitation(r, t.OrgID, i, token)
	}
	writeJSON(w, map[string]bool{"success": true})
}
func (s *HTTPServer) handleInvitationAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if s.lockout.RecordFailure("invitation-accept", auth.ClientIP(r, s.proxyTrust)).Locked {
		writeError(w, 429, "aguarde antes de tentar novamente")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Name     string `json:"displayName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	existingID := ""
	session := auth.TokenFromRequest(r)
	if session != "" {
		u, err := s.auth.ValidateToken(session)
		if err == nil && u.Active {
			if !s.auth.Store.CSRF(session, r.Header.Get("X-CSRF-Token")) {
				writeError(w, 403, "token CSRF inválido")
				return
			}
			existingID = u.ID
		}
	}
	id, err := s.teams.AcceptInvitation(r.Context(), body.Token, body.Password, body.Name, existingID)
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	s.tenants.invalidate(id)
	writeJSON(w, map[string]bool{"success": true})
}
func (s *HTTPServer) handleSeats(w http.ResponseWriter, r *http.Request) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var a teams.Allocation
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			writeError(w, 400, "invalid body")
			return
		}
		if err := s.teams.Allocate(r.Context(), t.OrgID, t.UserID, a, false); err != nil {
			writeTeamError(w, r, err)
			return
		}
		s.tenants.invalidate("")
		s.kickSSE(t.OrgID, "")
	} else if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	summary, err := s.teams.Summary(r.Context(), t.OrgID)
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeJSON(w, summary)
}
func (s *HTTPServer) handleAllowedLives(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	l, err := s.teams.AllowedLives(r.Context(), t.OrgID)
	if err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"lives": l})
}
func (s *HTTPServer) handleSeatPrice(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.RequireAdmin(w, r, s.auth)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Price int64 `json:"priceCents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if err := s.teams.SetPrice(r.Context(), u.ID, body.Price); err != nil {
		writeTeamError(w, r, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}
func (s *HTTPServer) handleTeamAdministration(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.RequireAdmin(w, r, s.auth)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/orgs/"), "/")
	if len(parts) < 2 || len(parts) > 4 {
		writeError(w, 404, "not found")
		return
	}
	org, action := parts[0], parts[1]
	if _, err := s.controller.Repository().GetOrganization(org); err != nil {
		writeError(w, 404, "organização não encontrada")
		return
	}
	switch action {
	case "invitations":
		if len(parts) == 2 && r.Method == http.MethodGet {
			items, err := s.teams.Invitations(r.Context(), org)
			if err != nil {
				writeTeamError(w, r, err)
				return
			}
			writeJSON(w, map[string]any{"invitations": items})
			return
		}
		if len(parts) == 3 && r.Method == http.MethodDelete {
			if _, _, err := s.teams.ChangeInvitation(r.Context(), org, u.ID, parts[2], false); err != nil {
				writeTeamError(w, r, err)
				return
			}
			writeJSON(w, map[string]bool{"success": true})
			return
		}
		if s.mailer == nil || !s.mailer.Enabled() || s.auth.SiteURL == "" {
			writeError(w, 503, "envio de e-mail indisponível")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if s.lockout.RecordFailure("invite:"+org, auth.ClientIP(r, s.proxyTrust)).Locked {
			writeError(w, 429, "aguarde antes de enviar")
			return
		}
		var i teams.Invitation
		var token string
		var err error
		if len(parts) == 4 && parts[3] == "resend" {
			i, token, err = s.teams.ChangeInvitation(r.Context(), org, u.ID, parts[2], true)
		} else if len(parts) == 2 {
			var body struct {
				Email string `json:"email"`
			}
			if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, 400, "invalid body")
				return
			}
			i, token, err = s.teams.CreateInvitation(r.Context(), org, u.ID, body.Email)
		} else {
			writeError(w, 404, "not found")
			return
		}
		if err != nil {
			writeTeamError(w, r, err)
			return
		}
		s.deliverInvitation(r, org, i, token)
		writeJSON(w, map[string]any{"invitation": i})

	case "allowed-lives":
		if r.Method == http.MethodPost {
			var l teams.AllowedLive
			if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
				writeError(w, 400, "invalid body")
				return
			}
			if err := s.teams.SetAllowedLive(r.Context(), org, u.ID, l); err != nil {
				writeTeamError(w, r, err)
				return
			}
			if !l.Active {
				name, _ := teams.NormalizeUsername(l.Username)
				s.controller.StopMonitoringLive(org, name)
			}
		} else if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		l, err := s.teams.AllowedLives(r.Context(), org)
		if err != nil {
			writeTeamError(w, r, err)
			return
		}
		writeJSON(w, map[string]any{"lives": l})
	case "seat-subscription":
		if r.Method == http.MethodPost {
			var p teams.Payment
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				writeError(w, 400, "invalid body")
				return
			}
			if err := s.teams.RecordPayment(r.Context(), org, u.ID, p); err != nil {
				writeTeamError(w, r, err)
				return
			}
			s.tenants.invalidate("")
			s.kickSSE(org, "")
		} else if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		summary, err := s.teams.Summary(r.Context(), org)
		if err != nil {
			writeTeamError(w, r, err)
			return
		}
		payments, err := s.teams.Payments(r.Context(), org)
		if err != nil {
			writeTeamError(w, r, err)
			return
		}
		writeJSON(w, map[string]any{"summary": summary, "payments": payments})
	case "allocation":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		var a teams.Allocation
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			writeError(w, 400, "invalid body")
			return
		}
		if err := s.teams.Allocate(r.Context(), org, u.ID, a, true); err != nil {
			writeTeamError(w, r, err)
			return
		}
		s.tenants.invalidate("")
		s.kickSSE(org, "")
		s.controller.EnforceAllowedLives(r.Context(), org)
		writeJSON(w, map[string]bool{"success": true})
	case "audit":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		a, err := s.teams.Audit(r.Context(), org)
		if err != nil {
			writeTeamError(w, r, err)
			return
		}
		writeJSON(w, map[string]any{"audit": a})
	default:
		writeError(w, 404, "not found")
	}
}
