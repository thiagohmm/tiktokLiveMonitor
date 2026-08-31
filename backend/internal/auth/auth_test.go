package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func signedTestToken(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func testAuthConfig() Config {
	return Config{
		Enabled:     true,
		JWTSecret:   "test-secret",
		JWTAudience: "authenticated",
		JWTIssuer:   "https://proj.supabase.co/auth/v1",
	}
}

func TestValidateTokenAcceptsExpectedAudAndIssuer(t *testing.T) {
	cfg := testAuthConfig()
	token := signedTestToken(t, cfg.JWTSecret, jwt.MapClaims{
		"sub": "11111111-1111-1111-1111-111111111111",
		"aud": "authenticated",
		"iss": cfg.JWTIssuer,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := cfg.ValidateToken(token); err != nil {
		t.Fatalf("token válido rejeitado: %v", err)
	}
}

func TestValidateTokenRejectsWrongAudience(t *testing.T) {
	cfg := testAuthConfig()
	token := signedTestToken(t, cfg.JWTSecret, jwt.MapClaims{
		"sub": "user-1",
		"aud": "other-project",
		"iss": cfg.JWTIssuer,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := cfg.ValidateToken(token); err == nil {
		t.Fatal("aud inesperado deveria invalidar o token")
	}
}

func TestValidateTokenRejectsWrongIssuer(t *testing.T) {
	cfg := testAuthConfig()
	token := signedTestToken(t, cfg.JWTSecret, jwt.MapClaims{
		"sub": "user-1",
		"aud": "authenticated",
		"iss": "https://evil.example/auth/v1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := cfg.ValidateToken(token); err == nil {
		t.Fatal("iss inesperado deveria invalidar o token")
	}
}

func TestValidateTokenRequiresHeaderTokenOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/events?access_token=abc", nil)
	if got := TokenFromRequest(req); got != "" {
		t.Fatalf("token na query string aceito: %q (deve ser ignorado)", got)
	}
	req.Header.Set("Authorization", "bearer abc")
	if got := TokenFromRequest(req); got != "abc" {
		t.Fatalf("Bearer header = %q, want abc", got)
	}
}

func TestTokenFromRequestAcceptsHTTPOnlyCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin.html", nil)
	req.AddCookie(&http.Cookie{Name: AccessTokenCookie, Value: "cookie-token", HttpOnly: true})
	if got := TokenFromRequest(req); got != "cookie-token" {
		t.Fatalf("cookie token = %q, want cookie-token", got)
	}
}

// TestAdminAPIRequiresAdminRole garante que os endpoints /api/admin/* só
// atendem admins (a página admin.html é servida pelo frontend, que aplica a
// mesma restrição no cliente).
func TestAdminAPIRequiresAdminRole(t *testing.T) {
	cfg := testAuthConfig()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := RequireAdmin(w, r, cfg); !ok {
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	t.Run("sem sessão recebe unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/lives", nil)
		rec := httptest.NewRecorder()
		cfg.Middleware(next).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("assinante recebe forbidden", func(t *testing.T) {
		token := signedTestToken(t, cfg.JWTSecret, jwt.MapClaims{
			"sub": "subscriber-1", "aud": "authenticated", "iss": cfg.JWTIssuer,
			"exp":          time.Now().Add(time.Hour).Unix(),
			"app_metadata": map[string]any{"role": "subscriber", "active": true},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/admin/lives", nil)
		req.AddCookie(&http.Cookie{Name: AccessTokenCookie, Value: token})
		rec := httptest.NewRecorder()
		cfg.Middleware(next).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("admin acessa", func(t *testing.T) {
		token := signedTestToken(t, cfg.JWTSecret, jwt.MapClaims{
			"sub": "admin-1", "aud": "authenticated", "iss": cfg.JWTIssuer,
			"exp":          time.Now().Add(time.Hour).Unix(),
			"app_metadata": map[string]any{"role": "admin", "active": true},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/admin/lives", nil)
		req.AddCookie(&http.Cookie{Name: AccessTokenCookie, Value: token})
		rec := httptest.NewRecorder()
		cfg.Middleware(next).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestValidateTokenExpired(t *testing.T) {
	cfg := testAuthConfig()
	token := signedTestToken(t, cfg.JWTSecret, jwt.MapClaims{
		"sub": "user-1",
		"aud": "authenticated",
		"iss": cfg.JWTIssuer,
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	if _, err := cfg.ValidateToken(token); err == nil {
		t.Fatal("token expirado aceito")
	}
}

func TestValidateTokenDefaultsAccountToPending(t *testing.T) {
	cfg := testAuthConfig()
	token := signedTestToken(t, cfg.JWTSecret, jwt.MapClaims{
		"sub": "user-without-app-metadata",
		"aud": "authenticated",
		"iss": cfg.JWTIssuer,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	user, err := cfg.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	if user.Active {
		t.Fatal("conta sem app_metadata.active deveria ficar pendente")
	}
}
