// Package auth validates Supabase JWT tokens and exposes request-scoped user claims.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const userContextKey contextKey = "authUser"

// User holds authenticated identity extracted from a Supabase access token.
type User struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Active bool   `json:"active"`
}

// Config controls Supabase-backed authentication for the API.
type Config struct {
	Enabled        bool
	JWTSecret      string
	SupabaseURL    string
	SupabaseAnon   string
	ServiceRoleKey string
}

// LoadConfigFromEnv builds auth settings from environment variables.
// Auth stays disabled when SUPABASE_JWT_SECRET is empty or AUTH_ENABLED=0.
func LoadConfigFromEnv() Config {
	enabled := strings.TrimSpace(os.Getenv("AUTH_ENABLED")) != "0"
	jwtSecret := strings.TrimSpace(os.Getenv("SUPABASE_JWT_SECRET"))
	if jwtSecret == "" {
		enabled = false
	}
	return Config{
		Enabled:        enabled,
		JWTSecret:      jwtSecret,
		SupabaseURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("SUPABASE_URL")), "/"),
		SupabaseAnon:   strings.TrimSpace(os.Getenv("SUPABASE_ANON_KEY")),
		ServiceRoleKey: strings.TrimSpace(os.Getenv("SUPABASE_SERVICE_ROLE_KEY")),
	}
}

// PublicPath reports whether a request path can be accessed without a token.
// Static web assets stay public; API routes and SSE require authentication.
func PublicPath(path string) bool {
	if path == "/login.html" || path == "/auth.js" || path == "/api/auth/config" || path == "/api/auth/login" || path == "/api/readiness" {
		return true
	}
	if strings.HasPrefix(path, "/vendor/") {
		return true
	}
	if path == "/events" || strings.HasPrefix(path, "/api/") {
		return false
	}
	return true
}

// TokenFromRequest reads a bearer token or ?access_token= for SSE clients.
func TokenFromRequest(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return strings.TrimSpace(r.URL.Query().Get("access_token"))
}

// ValidateToken parses and validates a Supabase JWT.
func (c Config) ValidateToken(tokenString string) (*User, error) {
	if !c.Enabled {
		return &User{Role: "admin", Active: true}, nil
	}
	if tokenString == "" {
		return nil, errors.New("token ausente")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("algoritmo inesperado: %v", token.Method.Alg())
		}
		return []byte(c.JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid {
		return nil, errors.New("token inválido")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("claims inválidas")
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, errors.New("usuário inválido")
	}

	email, _ := claims["email"].(string)
	role := "subscriber"
	active := true
	var subscriptionExpiresAt *time.Time

	if appMeta, ok := claims["app_metadata"].(map[string]any); ok {
		if v, ok := appMeta["role"].(string); ok && v != "" {
			role = v
		}
		if v, ok := appMeta["active"].(bool); ok {
			active = v
		}
		if v, ok := appMeta["subscription_expires_at"].(string); ok && v != "" {
			if parsed, err := time.Parse(time.RFC3339, v); err == nil {
				subscriptionExpiresAt = &parsed
			}
		}
	}

	if role == "subscriber" && subscriptionExpiresAt != nil && time.Now().After(*subscriptionExpiresAt) {
		return nil, errors.New("assinatura expirada")
	}

	exp, err := claims.GetExpirationTime()
	if err == nil && exp != nil && time.Now().After(exp.Time) {
		return nil, errors.New("token expirado")
	}

	return &User{ID: sub, Email: email, Role: role, Active: active}, nil
}

// Middleware protects HTTP handlers when auth is enabled.
func (c Config) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.Enabled {
			ctx := context.WithValue(r.Context(), userContextKey, &User{Role: "admin", Active: true})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
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
