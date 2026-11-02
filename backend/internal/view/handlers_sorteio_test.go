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
)

// sorteioTestRepo implements only the settings methods the raffle flows touch;
// the embedded interface keeps the model.Repository contract satisfied.
type sorteioTestRepo struct {
	model.Repository
	settings map[string]string
}

func (r *sorteioTestRepo) GetSetting(key string) (string, error) {
	return r.settings[key], nil
}

func (r *sorteioTestRepo) SetSetting(key, value string) error {
	r.settings[key] = value
	return nil
}

// newSorteioTestServer builds a controller + server with one idle monitor
// attached to (testOrg, "live1") — no bridge, no database.
func newSorteioTestServer(t *testing.T) (*HTTPServer, *sorteioTestRepo) {
	t.Helper()
	mon, err := monitor.New()
	if err != nil {
		t.Fatalf("new monitor: %v", err)
	}
	repo := &sorteioTestRepo{settings: make(map[string]string)}
	ctrl := controller.NewAppController(mon, repo)
	srv := &HTTPServer{controller: ctrl, sseClients: make(map[*sseClient]struct{})}
	if _, err := ctrl.GetMonitorManager().AttachMonitorForTest(testOrg, "live1"); err != nil {
		t.Fatalf("attach monitor: %v", err)
	}
	return srv, repo
}

// doSorteioRequest dispatches to the raffle handler of target, mimicking the mux.
func doSorteioRequest(t *testing.T, srv *HTTPServer, method, target string, body any) *httptest.ResponseRecorder {
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
	case "/api/sorteio":
		srv.handleSorteioState(rec, req)
	case "/api/sorteio/start":
		srv.handleSorteioStart(rec, req)
	case "/api/sorteio/stop":
		srv.handleSorteioStop(rec, req)
	case "/api/sorteio/reset":
		srv.handleSorteioReset(rec, req)
	case "/api/sorteio/draw":
		srv.handleSorteioDraw(rec, req)
	case "/api/sorteio/participants":
		srv.handleSorteioAddParticipant(rec, req)
	case "/api/sorteio/participants/remove":
		srv.handleSorteioRemoveParticipant(rec, req)
	default:
		t.Fatalf("unmapped route %s", target)
	}
	return rec
}

func decodeSorteioView(t *testing.T, rec *httptest.ResponseRecorder) sorteioStateView {
	t.Helper()
	var view sorteioStateView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return view
}

func TestSorteioStateInitial(t *testing.T) {
	srv, _ := newSorteioTestServer(t)

	rec := doSorteioRequest(t, srv, http.MethodGet, "/api/sorteio", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	view := decodeSorteioView(t, rec)
	if view.Active {
		t.Fatal("expected inactive raffle")
	}
	if view.Live != "live1" {
		t.Fatalf("expected resolved live live1, got %q", view.Live)
	}
	if view.Participants == nil || len(view.Participants) != 0 {
		t.Fatalf("expected empty participants, got %#v", view.Participants)
	}
}

func TestSorteioMethodGuards(t *testing.T) {
	srv, _ := newSorteioTestServer(t)
	for _, route := range []string{"/api/sorteio/start", "/api/sorteio/stop", "/api/sorteio/reset", "/api/sorteio/draw", "/api/sorteio/participants", "/api/sorteio/participants/remove"} {
		if rec := doSorteioRequest(t, srv, http.MethodGet, route, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: expected 405, got %d", route, rec.Code)
		}
	}
	if rec := doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("/api/sorteio: expected 405 for POST, got %d", rec.Code)
	}
}

func TestSorteioFullFlow(t *testing.T) {
	srv, repo := newSorteioTestServer(t)

	// Start com palavra-chave: ativa coleta e persiste a settings da org.
	rec := doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/start", map[string]string{"keyword": "Sorteio"})
	if rec.Code != http.StatusOK {
		t.Fatalf("start: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if view := decodeSorteioView(t, rec); !view.Active || view.Keyword != "Sorteio" {
		t.Fatalf("start returned %#v", view)
	}
	persisted := repo.settings["app:"+testOrg]
	if persisted == "" {
		t.Fatal("expected org settings persisted on start")
	}

	// Participantes manuais.
	rec = doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/participants", map[string]string{"nickname": "Maria"})
	if rec.Code != http.StatusOK {
		t.Fatalf("add: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var addResp struct {
		Added bool `json:"added"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &addResp)
	if !addResp.Added {
		t.Fatal("expected added=true")
	}
	doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/participants", map[string]string{"nickname": "João"})
	doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/participants", map[string]string{"nickname": "Maria"}) // dedupe

	rec = doSorteioRequest(t, srv, http.MethodGet, "/api/sorteio", nil)
	view := decodeSorteioView(t, rec)
	if len(view.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %#v", view.Participants)
	}

	// Sorteio de 2 vencedores esvazia a caixa.
	rec = doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/draw", map[string]int{"count": 2})
	if rec.Code != http.StatusOK {
		t.Fatalf("draw: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var drawResp struct {
		Winners []monitor.SorteioParticipant `json:"winners"`
		State   sorteioStateView             `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &drawResp); err != nil {
		t.Fatalf("decode draw: %v", err)
	}
	if len(drawResp.Winners) != 2 || len(drawResp.State.Participants) != 0 {
		t.Fatalf("unexpected draw response: %#v", drawResp)
	}

	// Pool insuficiente -> 400.
	rec = doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/draw", map[string]int{"count": 1})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on insufficient pool, got %d: %s", rec.Code, rec.Body.String())
	}

	// count inválido -> 400.
	rec = doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/draw", map[string]int{"count": 0})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on count=0, got %d", rec.Code)
	}

	// Remoção de participante.
	doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/participants", map[string]string{"nickname": "Carlos"})
	rec = doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/participants/remove", map[string]string{"uniqueId": "Carlos"})
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Reset limpa; stop desliga a coleta.
	rec = doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/reset", nil)
	if view := decodeSorteioView(t, rec); len(view.Participants) != 0 || len(view.Winners) != 0 {
		t.Fatalf("reset returned %#v", view)
	}
	rec = doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/stop", nil)
	if view := decodeSorteioView(t, rec); view.Active {
		t.Fatalf("stop returned %#v", view)
	}
}

func TestSorteioStartWithoutLiveConflicts(t *testing.T) {
	mon, err := monitor.New()
	if err != nil {
		t.Fatalf("new monitor: %v", err)
	}
	ctrl := controller.NewAppController(mon, &sorteioTestRepo{settings: make(map[string]string)})
	srv := &HTTPServer{controller: ctrl, sseClients: make(map[*sseClient]struct{})}

	rec := doSorteioRequest(t, srv, http.MethodPost, "/api/sorteio/start", map[string]string{"keyword": "sorteio"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without monitored live, got %d: %s", rec.Code, rec.Body.String())
	}
}
