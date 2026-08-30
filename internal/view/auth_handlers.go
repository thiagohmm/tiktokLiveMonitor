package view

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
)

func (s *HTTPServer) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	lockout := s.lockout.Config()
	writeJSON(w, map[string]any{
		"enabled":         s.auth.Enabled,
		"supabaseUrl":     s.auth.SupabaseURL,
		"supabaseAnonKey": s.auth.SupabaseAnon,
		"maxLoginAttempts": lockout.MaxAttempts,
		"lockoutMinutes":  int(lockout.Lockout.Minutes()),
		"theme": map[string]string{
			"pink": s.theme.Pink,
			"cyan": s.theme.Cyan,
			"bg":   s.theme.BG,
		},
	})
}

func (s *HTTPServer) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled {
		writeError(w, http.StatusBadRequest, "autenticação desativada")
		return
	}

	ip := auth.ClientIP(r)

	switch r.Method {
	case http.MethodGet:
		email := strings.TrimSpace(r.URL.Query().Get("email"))
		if email == "" {
			writeError(w, http.StatusBadRequest, "email é obrigatório")
			return
		}
		writeJSON(w, s.lockout.Status(email, ip))
		return
	case http.MethodPost:
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}

		email := strings.TrimSpace(strings.ToLower(body.Email))
		if email == "" || body.Password == "" {
			writeError(w, http.StatusBadRequest, "email e senha são obrigatórios")
			return
		}

		status := s.lockout.Status(email, ip)
		if status.Locked {
			w.WriteHeader(http.StatusTooManyRequests)
			writeJSON(w, map[string]any{
				"error":             "conta temporariamente bloqueada por excesso de tentativas",
				"locked":            true,
				"retryAfterSec":     status.RetryAfterSec,
				"remainingAttempts": 0,
			})
			return
		}

		session, err := s.auth.SignInWithPassword(email, body.Password)
		if err != nil {
			failStatus := s.lockout.RecordFailure(email, ip)
			payload := map[string]any{
				"error":             err.Error(),
				"remainingAttempts": failStatus.Remaining,
			}
			if failStatus.Locked {
				payload["locked"] = true
				payload["retryAfterSec"] = failStatus.RetryAfterSec
				w.WriteHeader(http.StatusTooManyRequests)
				writeJSON(w, payload)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, payload)
			return
		}

		s.lockout.RecordSuccess(email, ip)
		writeJSON(w, map[string]any{
			"session": map[string]any{
				"access_token":  session.AccessToken,
				"refresh_token": session.RefreshToken,
				"expires_in":    session.ExpiresIn,
				"token_type":    session.TokenType,
			},
		})
		return
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := auth.TokenFromRequest(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "não autorizado")
		return
	}

	if err := s.auth.SignOutGlobal(token); err != nil {
		writeError(w, http.StatusBadGateway, "falha ao encerrar sessão")
		return
	}

	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	user, ok := auth.UserFromContext(r.Context())
	if !ok || user == nil {
		if !s.auth.Enabled {
			writeJSON(w, map[string]any{
				"authenticated": false,
				"authEnabled":   false,
			})
			return
		}
		writeError(w, http.StatusUnauthorized, "não autorizado")
		return
	}

	resp := map[string]any{
		"authenticated": true,
		"authEnabled":   s.auth.Enabled,
		"id":            user.ID,
		"email":         user.Email,
		"role":          user.Role,
		"active":        user.Active,
	}

	if s.admin != nil && user.ID != "" {
		if profile, err := s.admin.GetProfileByID(user.ID); err == nil {
			resp["displayName"] = profile.DisplayName
			resp["notes"] = profile.Notes
			if profile.SubscriptionExpiresAt != nil {
				resp["subscriptionExpiresAt"] = profile.SubscriptionExpiresAt.UTC().Format(time.RFC3339)
			}
		}
	}

	writeJSON(w, resp)
}

func (s *HTTPServer) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.RequireAdmin(w, r, s.auth); !ok {
		return
	}
	if s.admin == nil {
		writeError(w, http.StatusServiceUnavailable, "supabase admin não configurado")
		return
	}

	switch r.Method {
	case http.MethodGet:
		users, err := s.admin.ListSubscribers()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"users": users})
	case http.MethodPost:
		var body auth.CreateSubscriberRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		user, err := s.admin.CreateSubscriber(body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, user)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleAdminUsersUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.RequireAdmin(w, r, s.auth); !ok {
		return
	}
	if s.admin == nil {
		writeError(w, http.StatusServiceUnavailable, "supabase admin não configurado")
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body auth.UpdateSubscriberRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	user, err := s.admin.UpdateSubscriber(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, user)
}

func (s *HTTPServer) handleAdminUsersDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.RequireAdmin(w, r, s.auth); !ok {
		return
	}
	if s.admin == nil {
		writeError(w, http.StatusServiceUnavailable, "supabase admin não configurado")
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.admin.DeleteSubscriber(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}
