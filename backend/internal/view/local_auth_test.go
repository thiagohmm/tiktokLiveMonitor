package view

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
)

func TestLocalLoginCookieAndCSRF(t *testing.T) {
	s, _, _, _ := setupTestServer(t)
	s.auth.Enabled = true
	s.auth.SiteURL = "https://app.example.com"
	active := true
	p, err := s.auth.Store.CreateSubscriber(auth.CreateSubscriberRequest{Email: "owner@example.com", Password: "safe password 12"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.auth.Store.UpdateSubscriber(auth.UpdateSubscriberRequest{ID: p.ID, Active: &active}); err != nil {
		t.Fatal(err)
	}
	p, err = s.auth.Store.GetProfileByID(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureOwnOrganization(*p); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"owner@example.com","password":"safe password 12"}`))
	request.Header.Set("Origin", s.auth.SiteURL)
	response := httptest.NewRecorder()
	s.auth.Middleware(http.HandlerFunc(s.handleAuthLogin)).ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("session cookie is not protected")
	}
	var data map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if _, exists := data["session"]; exists {
		t.Fatal("bearer token exposed to JavaScript")
	}
	csrf, _ := data["csrfToken"].(string)
	for _, tc := range []struct {
		name, origin, csrf string
		status             int
	}{{"valid", s.auth.SiteURL, csrf, 200}, {"missing csrf", s.auth.SiteURL, "", 403}, {"external origin", "https://other.example.com", csrf, 403}, {"missing origin", "", csrf, 403}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"targetGifts":[]}`))
			r.AddCookie(cookies[0])
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-CSRF-Token", tc.csrf)
			w := httptest.NewRecorder()
			s.auth.Middleware(s.tenantMiddleware(http.HandlerFunc(s.handleSettings))).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestOperatorCannotMutateAnyOperationalRoute(t *testing.T) {
	f := setupTenantFixture(t)
	for _, target := range []string{"/api/connect", "/api/disconnect", "/api/settings", "/api/goals", "/api/target-gift-history/answer", "/api/target-gift-history/priority", "/api/pix/tickets/1/messages", "/api/pix/tickets/1/claim", "/api/pix/tickets/1/answer", "/api/pix/values", "/api/monitoring/attach", "/api/monitoring/beacon-disconnect", "/api/org/invitations"} {
		t.Run(target, func(t *testing.T) {
			called := false
			rec := f.do(t, func(w http.ResponseWriter, r *http.Request) { called = true }, f.operatorA, http.MethodPost, target, `{}`)
			if rec.Code != 403 || called {
				t.Fatalf("operational mutation authorized: %d", rec.Code)
			}
		})
	}
	for _, target := range []string{"/api/disconnect", "/api/clear-history", "/api/monitoring/attach", "/api/pix/whatsapp/qr"} {
		rec := f.do(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("operator reached protected handler") }, f.operatorA, http.MethodGet, target, "")
		if rec.Code != 403 {
			t.Fatal(rec.Code)
		}
	}
}
