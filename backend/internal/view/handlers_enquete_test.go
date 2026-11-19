package view

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/tenant"
)

// doEnqueteRequest dispatches to the poll handler of target, mimicking the mux.
func doEnqueteRequest(t *testing.T, srv *HTTPServer, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		raw = encoded
	}
	req := newOrgRequest(method, target, bytes.NewReader(raw))
	rec := httptest.NewRecorder()

	switch target {
	case "/api/enquete":
		srv.handleEnqueteState(rec, req)
	case "/api/enquete/start":
		srv.handleEnqueteStart(rec, req)
	case "/api/enquete/stop":
		srv.handleEnqueteStop(rec, req)
	case "/api/enquete/reset":
		srv.handleEnqueteReset(rec, req)
	default:
		t.Fatalf("unmapped route %s", target)
	}
	return rec
}

func decodeEnqueteView(t *testing.T, rec *httptest.ResponseRecorder) enqueteStateView {
	t.Helper()
	var view enqueteStateView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return view
}

func TestEnqueteStateInitial(t *testing.T) {
	srv, _ := newSorteioTestServer(t)

	rec := doEnqueteRequest(t, srv, http.MethodGet, "/api/enquete", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	view := decodeEnqueteView(t, rec)
	if view.Active {
		t.Fatal("expected inactive poll")
	}
	if view.Live != "live1" {
		t.Fatalf("expected resolved live live1, got %q", view.Live)
	}
	if view.Options == nil || len(view.Options) != 0 {
		t.Fatalf("expected empty options, got %#v", view.Options)
	}
}

func TestEnqueteMethodGuards(t *testing.T) {
	srv, _ := newSorteioTestServer(t)
	for _, route := range []string{"/api/enquete/start", "/api/enquete/stop", "/api/enquete/reset"} {
		if rec := doEnqueteRequest(t, srv, http.MethodGet, route, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: expected 405, got %d", route, rec.Code)
		}
	}
	if rec := doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("/api/enquete: expected 405 for POST, got %d", rec.Code)
	}
}

func TestEnqueteFullFlow(t *testing.T) {
	srv, _ := newSorteioTestServer(t)

	// Sem duas palavras não há enquete (400, não 500).
	rec := doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete/start", map[string]any{"words": []string{"sim"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start com 1 palavra: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete/start", map[string]any{
		"question": "Vai chover hoje?",
		"words":    []string{"Sim", "Não"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("start: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	view := decodeEnqueteView(t, rec)
	if !view.Active || view.Question != "Vai chover hoje?" || len(view.Options) != 2 {
		t.Fatalf("unexpected start view: %#v", view)
	}
	if view.Options[0].Word != "Sim" || view.Options[1].Word != "Não" {
		t.Fatalf("expected operator spelling preserved, got %#v", view.Options)
	}

	// Segunda enquete sem encerrar/limpar: 409.
	rec = doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete/start", map[string]any{"words": []string{"a", "b"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("start com enquete ativa: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// GET reflete o estado aberto.
	rec = doEnqueteRequest(t, srv, http.MethodGet, "/api/enquete", nil)
	if view := decodeEnqueteView(t, rec); !view.Active || view.Total != 0 {
		t.Fatalf("unexpected state view: %#v", view)
	}

	// Stop encerra mantendo opções.
	rec = doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete/stop", nil)
	if view := decodeEnqueteView(t, rec); view.Active || len(view.Options) != 2 {
		t.Fatalf("stop deve encerrar mantendo opções, got %#v", view)
	}

	// Reset limpa opções e resultado, permitindo nova enquete.
	rec = doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete/reset", nil)
	if view := decodeEnqueteView(t, rec); view.Active || len(view.Options) != 0 || view.Total != 0 || view.Winner != "" {
		t.Fatalf("reset retornou %#v", view)
	}
	rec = doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete/start", map[string]any{"words": []string{"azul", "verde"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("start após reset: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A enquete de uma org não é visível para outra (isolamento multi-tenant).
func TestEnqueteIsIsolatedPerOrg(t *testing.T) {
	srv, _ := newSorteioTestServer(t)
	if rec := doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete/start", map[string]any{"words": []string{"sim", "não"}}); rec.Code != http.StatusOK {
		t.Fatalf("start: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	mon, err := monitor.New()
	if err != nil {
		t.Fatalf("new monitor: %v", err)
	}
	otherCtrl := controller.NewAppController(mon, &sorteioTestRepo{settings: make(map[string]string)})
	otherSrv := &HTTPServer{controller: otherCtrl, sseClients: make(map[*sseClient]struct{})}
	if _, err := otherCtrl.GetMonitorManager().AttachMonitorForTest("org-enquete-b", "live-b"); err != nil {
		t.Fatalf("attach monitor: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/enquete", nil)
	req = req.WithContext(tenant.With(req.Context(), tenant.Tenant{
		OrgID: "org-enquete-b", UserID: "owner-b", Role: model.OrgRoleOwner,
	}))
	rec := httptest.NewRecorder()
	otherSrv.handleEnqueteState(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if view := decodeEnqueteView(t, rec); view.Active || len(view.Options) != 0 {
		t.Fatalf("org B não deve ver a enquete da org A: %#v", view)
	}
}

func TestEnqueteStartWithoutLiveConflicts(t *testing.T) {
	mon, err := monitor.New()
	if err != nil {
		t.Fatalf("new monitor: %v", err)
	}
	ctrl := controller.NewAppController(mon, &sorteioTestRepo{settings: make(map[string]string)})
	srv := &HTTPServer{controller: ctrl, sseClients: make(map[*sseClient]struct{})}

	rec := doEnqueteRequest(t, srv, http.MethodPost, "/api/enquete/start", map[string]any{"words": []string{"sim", "não"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without monitored live, got %d: %s", rec.Code, rec.Body.String())
	}
}
