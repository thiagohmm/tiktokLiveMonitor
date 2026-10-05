package view

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
)

// TestTenantExemptAdminCoversOnlyPlatformAdminRoutes garante que apenas as
// rotas de administracao da plataforma pulam a resolucao de organizacao.
// Qualquer rota nova sob outros prefixos continua exigindo org/seat.
func TestTenantExemptAdminCoversOnlyPlatformAdminRoutes(t *testing.T) {
	exempt := []string{
		"/api/admin/users",
		"/api/admin/users/update",
		"/api/admin/users/delete",
		"/api/admin/orgs",
		"/api/admin/orgs/update",
		"/api/admin/orgs/members",
		"/api/admin/orgs/123/seats",
		"/api/admin/lives/assign",
	}
	for _, p := range exempt {
		if !tenantExemptAdmin(p) {
			t.Errorf("tenantExemptAdmin(%q) = false; esperado true", p)
		}
		if !tenantExempt(p) {
			t.Errorf("tenantExempt(%q) = false; esperado true", p)
		}
	}

	notExempt := []string{
		"/api/admin/lives",             // rota do dono da org, exige tenant
		"/api/admin/lives/session/delete",
		"/api/admin/billing/price",
		"/api/org",
		"/api/org/members",
		"/api/pix/tickets",
		"/api/state",
	}
	for _, p := range notExempt {
		if tenantExemptAdmin(p) {
			t.Errorf("tenantExemptAdmin(%q) = true; a rota não é de admin da plataforma", p)
		}
	}

	// Endpoints de sessao continuam isentos (precisam funcionar sem org).
	for _, p := range []string{"/api/auth/me", "/api/auth/logout"} {
		if !tenantExempt(p) {
			t.Errorf("tenantExempt(%q) = false; endpoints de sessão devem ser isentos", p)
		}
		if tenantExemptAdmin(p) {
			t.Errorf("tenantExemptAdmin(%q) = true; não é rota de admin", p)
		}
	}
}

// TestReadinessDoesNotLeakInternalMetrics cobre P6: o endpoint publico de
// prontidao nao pode expor contagem de goroutines nem de clientes SSE.
func TestReadinessDoesNotLeakInternalMetrics(t *testing.T) {
	s := &HTTPServer{}
	rec := httptest.NewRecorder()
	s.handleReadiness(rec, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; esperado 200", rec.Code)
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"goroutines", "sseClients"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("resposta pública de readiness expõe %q: %s", forbidden, body)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("resposta não é JSON: %v", err)
	}
	if ready, ok := payload["ready"].(bool); !ok || !ready {
		t.Errorf("payload = %v; esperado {\"ready\":true}", payload)
	}
}

// TestReadinessAndStateRejectNonGet cobre L1-4: os dois handlers nao tinham
// guard de metodo.
func TestReadinessAndStateRejectNonGet(t *testing.T) {
	s := &HTTPServer{}

	rec := httptest.NewRecorder()
	s.handleReadiness(rec, httptest.NewRequest(http.MethodPost, "/api/readiness", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/readiness = %d; esperado 405", rec.Code)
	}

	// Sem tenant no contexto o handler responderia 401; o guard de metodo tem
	// de responder antes disso.
	rec = httptest.NewRecorder()
	s.handleState(rec, httptest.NewRequest(http.MethodDelete, "/api/state", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /api/state = %d; esperado 405", rec.Code)
	}
}

// TestOriginCheckNormalizesTrailingSlash cobre L2-11: SITE_URL e carregado sem
// barra final e o browser pode enviar a origem com ou sem ela em cabecalhos
// equivalentes. O que NAO pode acontecer e aceitar origem estranha.
func TestOriginCheckNormalizesTrailingSlash(t *testing.T) {
	const site = "https://livemonitortk.com.br"

	server := &HTTPServer{auth: auth.Config{Enabled: true, SiteURL: site}}
	handler := server.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		origin string
		want   int
	}{
		// 401 = passou pela checagem de origem e parou por falta de sessao.
		// 403 = bloqueado pela checagem de origem. E essa distincao que importa.
		{"origem exata", site, http.StatusUnauthorized},
		{"origem com barra final", site + "/", http.StatusUnauthorized},
		{"origem ausente", "", http.StatusForbidden},
		{"origem estranha", "https://evil.example", http.StatusForbidden},
		{"subdominio parecido", "https://livemonitortk.com.br.evil.example", http.StatusForbidden},
		{"esquema diferente", "http://livemonitortk.com.br", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader("{}"))
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("Origin=%q -> %d; esperado %d", tc.origin, rec.Code, tc.want)
			}
			if tc.want == http.StatusForbidden && !strings.Contains(rec.Body.String(), "origem") {
				t.Errorf("Origin=%q foi bloqueada por outro motivo: %s", tc.origin, rec.Body.String())
			}
		})
	}
}
