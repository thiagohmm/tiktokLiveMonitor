package view

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func adminAuthorizationTestServer(t *testing.T, profiles []map[string]any) *HTTPServer {
	t.Helper()
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/auth/v1/user":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "admin-1",
				"email": "admin@example.com",
				"app_metadata": map[string]any{
					"role": "admin", "active": true,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/profiles":
			_ = json.NewEncoder(w).Encode(profiles)
		default:
			http.Error(w, "unexpected Supabase request", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(supabase.Close)

	cfg := auth.Config{Enabled: true, SupabaseURL: supabase.URL, SupabaseAnon: "anon", ServiceRoleKey: "service-role"}
	return &HTTPServer{auth: cfg, admin: auth.NewAdminClient(cfg)}
}

func authenticatedAdminRequest(srv *HTTPServer, handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.auth.Middleware(handler).ServeHTTP(rec, req)
	return rec
}

func TestHandleAdminUsersHidesAuthenticatedAdminEvenWhenProfileRoleIsStale(t *testing.T) {
	now := time.Now().UTC()
	srv := adminAuthorizationTestServer(t, []map[string]any{
		{"id": "admin-1", "email": "admin@example.com", "role": "subscriber", "active": true, "created_at": now, "updated_at": now},
		{"id": "subscriber-1", "email": "user@example.com", "role": "subscriber", "active": false, "created_at": now, "updated_at": now},
	})

	rec := authenticatedAdminRequest(srv, srv.handleAdminUsers, http.MethodGet, "/api/admin/users", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Users []auth.SubscriberProfile `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(payload.Users) != 1 || payload.Users[0].ID != "subscriber-1" {
		t.Fatalf("users=%+v, want only subscriber-1", payload.Users)
	}
}

// recoverTestCalls registra as chamadas que o handler fez ao mock do Supabase.
type recoverTestCalls struct {
	mu             sync.Mutex
	generateLink   []map[string]any
	passwordPuts   []map[string]any
	passwordPutHdr []http.Header
}

func (c *recoverTestCalls) recordGenerateLink(body map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generateLink = append(c.generateLink, body)
}

func (c *recoverTestCalls) recordPasswordPut(h http.Header, body map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.passwordPuts = append(c.passwordPuts, body)
	c.passwordPutHdr = append(c.passwordPutHdr, h.Clone())
}

// recoverTestServer monta um HTTPServer com um mock do Supabase que atende
// POST /auth/v1/admin/generate_link (recovery) e PUT /auth/v1/user.
// knownEmails lista os e-mails que o generate_link aceita; os demais
// recebem 422, como o Supabase real para usuário inexistente.
func recoverTestServer(t *testing.T, knownEmails ...string) (*HTTPServer, *recoverTestCalls) {
	t.Helper()
	known := make(map[string]bool, len(knownEmails))
	for _, e := range knownEmails {
		known[e] = true
	}
	calls := &recoverTestCalls{}
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/admin/generate_link":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			calls.recordGenerateLink(body)
			email, _ := body["email"].(string)
			if !known[email] {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_ = json.NewEncoder(w).Encode(map[string]string{"msg": "User not found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"action_link": "https://project-ref.supabase.co/auth/v1/verify?token=rec-token&type=recovery",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/auth/v1/user":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			calls.recordPasswordPut(r.Header, body)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "u1", "email": "cliente@example.com"})
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
		SiteURL:        "https://tlm.example.com",
	}
	srv := &HTTPServer{
		auth:    cfg,
		admin:   auth.NewAdminClient(cfg),
		lockout: auth.NewLoginLockout(auth.LockoutConfig{MaxAttempts: 3, Lockout: time.Minute}),
	}
	return srv, calls
}

func TestHandleAuthRecoverAlwaysRespondsGenericOK(t *testing.T) {
	srv, calls := recoverTestServer(t, "cliente@example.com")

	// E-mail cadastrado e inexistente devem devolver exatamente a mesma
	// resposta genérica (anti-enumeração).
	for _, email := range []string{"cliente@example.com", "desconhecido@example.com"} {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/recover", strings.NewReader(
			`{"email":"`+email+`"}`))
		req.RemoteAddr = "203.0.113.7:1234"
		rec := httptest.NewRecorder()
		srv.handleAuthRecover(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", email, rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s: json: %v", email, err)
		}
		msg, _ := payload["message"].(string)
		if !strings.Contains(msg, "Se este e-mail estiver cadastrado") {
			t.Fatalf("%s: mensagem não é genérica: %q", email, msg)
		}
	}

	// O body enviado ao Supabase deve pedir recovery com redirect para o
	// reset-password.html do SITE_URL.
	if len(calls.generateLink) != 2 {
		t.Fatalf("generate_link chamado %d vezes, want 2", len(calls.generateLink))
	}
	for i, body := range calls.generateLink {
		if body["type"] != "recovery" {
			t.Fatalf("chamada %d: type=%v, want recovery", i+1, body["type"])
		}
		opts, _ := body["options"].(map[string]any)
		if opts["redirect_to"] != "https://tlm.example.com/reset-password.html" {
			t.Fatalf("chamada %d: redirect_to=%v", i+1, opts["redirect_to"])
		}
	}
}

func TestHandleAuthRecoverRateLimitsPerIP(t *testing.T) {
	srv, _ := recoverTestServer(t, "cliente@example.com")
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/recover", strings.NewReader(
			`{"email":"cliente@example.com"}`))
		req.RemoteAddr = "203.0.113.10:1234"
		rec := httptest.NewRecorder()
		srv.handleAuthRecover(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/recover", strings.NewReader(
		`{"email":"cliente@example.com"}`))
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()
	srv.handleAuthRecover(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429 after recover cap", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if payload["locked"] != true {
		t.Fatalf("locked=%v, want true", payload["locked"])
	}
	if retry, ok := payload["retryAfterSec"].(float64); !ok || retry <= 0 {
		t.Fatalf("retryAfterSec=%v, want > 0", payload["retryAfterSec"])
	}
}

func TestHandleAuthResetPasswordRejectsShortPassword(t *testing.T) {
	srv, calls := recoverTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", strings.NewReader(
		`{"token":"rec-token","password":"curta"}`))
	rec := httptest.NewRecorder()
	srv.handleAuthResetPassword(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "senha deve ter pelo menos 8 caracteres") {
		t.Fatalf("body=%s", rec.Body.String())
	}
	if len(calls.passwordPuts) != 0 {
		t.Fatalf("PUT /auth/v1/user chamado com senha curta: %+v", calls.passwordPuts)
	}
}

func TestHandleAuthResetPasswordSuccessCallsSupabaseUserPut(t *testing.T) {
	srv, calls := recoverTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", strings.NewReader(
		`{"token":"rec-token","password":"novaSenha123"}`))
	rec := httptest.NewRecorder()
	srv.handleAuthResetPassword(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !payload["success"] {
		t.Fatalf("success=%v, want true", payload["success"])
	}
	if len(calls.passwordPuts) != 1 {
		t.Fatalf("PUT /auth/v1/user chamado %d vezes, want 1", len(calls.passwordPuts))
	}
	if calls.passwordPuts[0]["password"] != "novaSenha123" {
		t.Fatalf("password=%v, want novaSenha123", calls.passwordPuts[0]["password"])
	}
	hdr := calls.passwordPutHdr[0]
	if hdr.Get("apikey") != "anon" {
		t.Fatalf("apikey=%q, want anon", hdr.Get("apikey"))
	}
	if hdr.Get("Authorization") != "Bearer rec-token" {
		t.Fatalf("Authorization=%q, want Bearer rec-token", hdr.Get("Authorization"))
	}
}

func TestHandleAuthResetPasswordInvalidTokenReturnsGenericError(t *testing.T) {
	// Mock que rejeita o PUT (token inválido/expirado/usado) com o detalhe
	// que o Supabase real devolveria — ele não pode vazar para o cliente.
	supabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid login or password"})
	}))
	t.Cleanup(supabase.Close)

	cfg := auth.Config{Enabled: true, SupabaseURL: supabase.URL, SupabaseAnon: "anon"}
	srv := &HTTPServer{auth: cfg}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", strings.NewReader(
		`{"token":"token-ja-usado","password":"novaSenha123"}`))
	rec := httptest.NewRecorder()
	srv.handleAuthResetPassword(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "link inválido ou expirado") {
		t.Fatalf("body=%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Invalid login") {
		t.Fatalf("detalhe do Supabase vazou para o cliente: %s", rec.Body.String())
	}
}

func TestHandleAdminUsersRejectsOwnAccountMutation(t *testing.T) {
	srv := adminAuthorizationTestServer(t, nil)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
		body    string
	}{
		{"update", srv.handleAdminUsersUpdate, http.MethodPatch, "/api/admin/users/update", `{"id":"admin-1","active":false}`},
		{"delete", srv.handleAdminUsersDelete, http.MethodPost, "/api/admin/users/delete?id=admin-1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := authenticatedAdminRequest(srv, tt.handler, tt.method, tt.target, tt.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
