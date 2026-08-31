package view

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
)

func signupTestServer(t *testing.T) (*HTTPServer, *httptest.Server) {
	t.Helper()
	var profiles []map[string]any
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/admin/users":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			meta, _ := body["app_metadata"].(map[string]any)
			if meta["role"] != "subscriber" || meta["active"] != false {
				t.Errorf("cadastro público com metadata inesperada: %+v", meta)
			}
			id := "11111111-1111-1111-1111-111111111111"
			now := time.Now().UTC()
			profiles = []map[string]any{{
				"id": id, "email": body["email"], "display_name": "",
				"role": "subscriber", "active": false, "notes": "",
				"created_at": now, "updated_at": now,
			}}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/rest/v1/profiles"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/v1/profiles"):
			_ = json.NewEncoder(w).Encode(profiles)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(supabase.Close)

	cfg := auth.Config{
		Enabled:        true,
		SupabaseURL:    supabase.URL,
		ServiceRoleKey: "service-role",
		SupabaseAnon:   "anon",
	}
	return &HTTPServer{
		auth:    cfg,
		admin:   auth.NewAdminClient(cfg),
		lockout: auth.NewLoginLockout(auth.LockoutConfig{MaxAttempts: 3, Lockout: time.Minute}),
	}, supabase
}

func TestHandleAuthSignupCreatesPendingAccount(t *testing.T) {
	srv, _ := signupTestServer(t)
	body := `{"email":"cliente@example.com","password":"secret123","displayName":"Ana","notes":"PIX Ana","role":"admin","active":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleAuthSignup(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if payload["pending"] != true {
		t.Fatalf("pending=%v, want true", payload["pending"])
	}
}

func TestHandleAuthSignupMethodNotAllowed(t *testing.T) {
	srv, _ := signupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/signup", nil)
	rec := httptest.NewRecorder()
	srv.handleAuthSignup(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405", rec.Code)
	}
}

func TestHandleAuthSignupRateLimitsPerIP(t *testing.T) {
	srv, _ := signupTestServer(t)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", strings.NewReader(
			`{"email":"c`+string(rune('a'+i))+`@example.com","password":"secret123"}`,
		))
		req.RemoteAddr = "203.0.113.10:1234"
		rec := httptest.NewRecorder()
		srv.handleAuthSignup(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("attempt %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", strings.NewReader(
		`{"email":"extra@example.com","password":"secret123"}`,
	))
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()
	srv.handleAuthSignup(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429 after signup cap", rec.Code)
	}
}
