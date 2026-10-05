// Package auth implements PostgreSQL-backed local authentication.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
)

type contextKey string

const userContextKey contextKey = "authUser"
const AccessTokenCookie = "tlm_session"

type User struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Active bool   `json:"active"`
}
type Config struct {
	Enabled bool
	SiteURL string
	Store   *Store
}

func LoadConfigFromEnv() Config {
	return Config{Enabled: strings.TrimSpace(os.Getenv("AUTH_ENABLED")) != "0", SiteURL: strings.TrimRight(strings.TrimSpace(os.Getenv("SITE_URL")), "/")}
}
func CheckConfigFromEnv() error {
	if os.Getenv("AUTH_ENABLED") == "0" {
		if strings.HasPrefix(os.Getenv("SITE_URL"), "https://") {
			return errors.New("AUTH_ENABLED=0 é permitido apenas em desenvolvimento local")
		}
		return nil
	}
	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		return errors.New("DATABASE_URL obrigatório para autenticação local")
	}
	return nil
}

// PublicPath reports whether a request path can be accessed without a token.
// O backend é uma API pura: apenas a configuração pública de login e o
// readiness são acessíveis sem token; todo o resto (/api/* e /events) exige
// autenticação. Os arquivos da UI não são mais servidos pelo backend.
func PublicPath(path string) bool {
	if path == "/api/auth/config" || path == "/api/auth/login" || path == "/api/auth/signup" ||
		path == "/api/auth/recover" || path == "/api/auth/reset-password" || path == "/api/auth/invitations/accept" || path == "/api/readiness" ||
		path == "/api/webhooks/whatsapp" {
		return true
	}
	if path == "/events" || strings.HasPrefix(path, "/api/") {
		return false
	}
	return true
}

// TokenFromRequest reads a bearer token from the Authorization header.
// Tokens in the URL (?access_token=) are rejected to avoid leaking them
// into logs, Referer headers and browser history.
func TokenFromRequest(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	if cookie, err := r.Cookie(AccessTokenCookie); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

// Middleware protects HTTP handlers when auth is enabled. A página /admin.html
// não é mais servida pelo backend (a UI vive em /frontend); a restrição de
// papel admin é aplicada nos endpoints /api/admin/* via RequireAdmin.
func (c Config) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.Enabled {
			ctx := context.WithValue(r.Context(), userContextKey, &User{Role: "admin", Active: true})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && r.URL.Path != "/api/webhooks/whatsapp" {
			// Compara origem normalizada: o browser pode enviar (ou omitir) a
			// barra final, e SITE_URL e carregado sem barra.
			origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
			if c.SiteURL != "" && origin != strings.TrimRight(c.SiteURL, "/") {
				writeAuthError(w, http.StatusForbidden, "origem não autorizada")
				return
			}
		}
		if PublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		user, err := c.ValidateToken(TokenFromRequest(r))
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "não autorizado")
			return
		}
		if !user.Active {
			writeAuthError(w, http.StatusForbidden, "conta desativada")
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if c.Store == nil || !c.Store.CSRF(TokenFromRequest(r), r.Header.Get("X-CSRF-Token")) {
				writeAuthError(w, http.StatusForbidden, "token CSRF inválido")
				return
			}
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserFromContext returns the authenticated user, if any.
func UserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userContextKey).(*User)
	return user, ok
}

// RequireAdmin ensures the caller is an active admin user.
func RequireAdmin(w http.ResponseWriter, r *http.Request, cfg Config) (*User, bool) {
	if !cfg.Enabled {
		return &User{Role: "admin", Active: true}, true
	}
	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		writeAuthError(w, http.StatusUnauthorized, "não autorizado")
		return nil, false
	}
	if user.Role != "admin" {
		writeAuthError(w, http.StatusForbidden, "acesso negado")
		return nil, false
	}
	return user, true
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
