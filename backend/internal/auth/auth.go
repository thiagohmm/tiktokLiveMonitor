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

const AccessTokenCookie = "tlm_access_token"

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
	JWTAudience    string
	JWTIssuer      string
}

// LoadConfigFromEnv builds auth settings from environment variables.
// Auth stays disabled when SUPABASE_JWT_SECRET is empty or AUTH_ENABLED=0.
func LoadConfigFromEnv() Config {
	enabled := strings.TrimSpace(os.Getenv("AUTH_ENABLED")) != "0"
	jwtSecret := strings.TrimSpace(os.Getenv("SUPABASE_JWT_SECRET"))
	if jwtSecret == "" {
		enabled = false
	}
	supabaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("SUPABASE_URL")), "/")

	audience := strings.TrimSpace(os.Getenv("SUPABASE_JWT_AUD"))
	if audience == "" {
		audience = "authenticated"
	}
	issuer := strings.TrimSpace(os.Getenv("SUPABASE_JWT_ISSUER"))
	if issuer == "" && supabaseURL != "" {
		issuer = supabaseURL + "/auth/v1"
	}

	return Config{
		Enabled:        enabled,
		JWTSecret:      jwtSecret,
		SupabaseURL:    supabaseURL,
		SupabaseAnon:   strings.TrimSpace(os.Getenv("SUPABASE_ANON_KEY")),
		ServiceRoleKey: strings.TrimSpace(os.Getenv("SUPABASE_SERVICE_ROLE_KEY")),
		JWTAudience:    audience,
		JWTIssuer:      issuer,
	}
}

// PublicPath reports whether a request path can be accessed without a token.
// O backend é uma API pura: apenas a configuração pública de login e o
// readiness são acessíveis sem token; todo o resto (/api/* e /events) exige
// autenticação. Os arquivos da UI não são mais servidos pelo backend.
func PublicPath(path string) bool {
	if path == "/api/auth/config" || path == "/api/auth/login" || path == "/api/auth/signup" || path == "/api/readiness" {
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

// ValidateToken parses and validates a Supabase JWT.
func (c Config) ValidateToken(tokenString string) (*User, error) {
	if !c.Enabled {
		return &User{Role: "admin", Active: true}, nil
	}
	if tokenString == "" {
		return nil, errors.New("token ausente")
	}

	parseOpts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	}
	if c.JWTIssuer != "" {
		parseOpts = append(parseOpts, jwt.WithIssuer(c.JWTIssuer))
	}
	if c.JWTAudience != "" {
		parseOpts = append(parseOpts, jwt.WithAudience(c.JWTAudience))
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("algoritmo inesperado: %v", token.Method.Alg())
		}
		return []byte(c.JWTSecret), nil
	}, parseOpts...)
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
	// Contas sem a claim explícita ficam pendentes por padrão. Isso impede que
	// um usuário criado fora do fluxo administrativo ganhe acesso por acidente.
	active := false
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
