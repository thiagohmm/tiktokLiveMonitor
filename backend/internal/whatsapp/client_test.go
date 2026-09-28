package whatsapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Fake WAHA ---

// wahaState is one answer of the fake gateway for one session.
type wahaState struct {
	Status string
	State  string
	Phone  string
}

// waha counts what the client does, because "it restarted my number" and "it did
// nothing" look identical to the user until the QR never arrives.
type waha struct {
	mu           sync.Mutex
	states       map[string][]wahaState
	contacts     map[string]string
	creates      int
	restarts     int
	logouts      int
	lastQRAccept string
	qrAlwaysJSON bool
}

// qrPNG is a tiny body with the real PNG signature, so a test can tell an image
// apart from the base64 JSON envelope WAHA can answer with instead.
var qrPNG = []byte("\x89PNG\r\n\x1a\nFAKE")

// newWaha registers sessions: those listed are paired, any other name is unknown
// until the client creates it.
func newWaha(paired ...string) *waha {
	gateway := &waha{states: map[string][]wahaState{}, contacts: map[string]string{}}
	for _, session := range paired {
		gateway.states[session] = []wahaState{{Status: "WORKING", Phone: "5511999990000"}}
	}
	return gateway
}

func (w *waha) serve() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		switch {
		case req.Method == http.MethodPost && path == "/api/sessions/":
			w.mu.Lock()
			w.creates++
			w.mu.Unlock()
			rw.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = rw.Write([]byte(`{"error":"Session 'x' already exists. Use PUT to update it."}`))

		case req.Method == http.MethodPost && strings.HasSuffix(path, "/restart"):
			w.mu.Lock()
			w.restarts++
			w.mu.Unlock()
			rw.WriteHeader(http.StatusCreated)

		case req.Method == http.MethodPost && strings.HasSuffix(path, "/logout"):
			w.mu.Lock()
			w.logouts++
			w.mu.Unlock()
			rw.WriteHeader(http.StatusCreated)

		case strings.HasSuffix(path, "/auth/qr"):
			w.mu.Lock()
			w.lastQRAccept = req.Header.Get("Accept")
			w.mu.Unlock()
			if !w.canServeQR(sessionOf(path)) {
				rw.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = rw.Write([]byte(`{"error":"Session status is not as expected. Try again later or restart the session"}`))
				return
			}
			if w.qrWantsJSON(w.lastQRAccept) {
				rw.Header().Set("Content-Type", "application/json")
				_, _ = rw.Write([]byte(`{"mimetype":"image/png","data":"` + base64.StdEncoding.EncodeToString(qrPNG) + `"}`))
				return
			}
			rw.Header().Set("Content-Type", "image/png")
			_, _ = rw.Write(qrPNG)

		case req.Method == http.MethodGet && strings.HasPrefix(path, "/api/sessions/"):
			session := sessionOf(path)
			state, ok := w.nextState(session)
			if !ok {
				rw.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(rw).Encode(state.body())

		case req.Method == http.MethodGet && strings.Contains(path, "/contacts/"):
			id := path[strings.LastIndex(path, "/")+1:]
			if body, ok := w.contacts[id]; ok {
				_, _ = rw.Write([]byte(body))
				return
			}
			rw.WriteHeader(http.StatusNotFound)

		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	})
}

func (w *waha) nextState(session string) (wahaState, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	states, ok := w.states[session]
	if !ok || len(states) == 0 {
		return wahaState{}, false
	}
	if len(states) > 1 {
		current := states[0]
		w.states[session] = states[1:]
		return current, true
	}
	return states[0], true
}

// qrWantsJSON mirrors WAHA: a JSON Accept gets the base64 envelope, an image
// Accept gets the PNG itself.
func (w *waha) qrWantsJSON(accept string) bool {
	if w.qrAlwaysJSON {
		return true
	}
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "image/png")
}

func (w *waha) canServeQR(session string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.states[session]; !ok {
		return false
	}
	current := w.states[session][0]
	return current.Status == StatusScanQRCode || current.State == StatusScanQRCode
}

func (s wahaState) body() map[string]any {
	body := map[string]any{"status": s.Status}
	if s.State != "" {
		body["engine"] = map[string]any{"state": s.State}
	}
	if s.Phone != "" {
		body["me"] = map[string]any{"id": s.Phone + "@c.us", "phone": s.Phone}
	}
	return body
}

// sessionOf reads the session name out of /api/sessions/{name}... or /api/{name}/auth/qr.
func sessionOf(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if parts[1] == "sessions" {
		return parts[2]
	}
	return parts[1]
}

func newTestClient(t *testing.T, gateway *waha) *Client {
	t.Helper()
	server := httptest.NewServer(gateway.serve())
	t.Cleanup(server.Close)
	return NewClient(Config{
		Enabled:        true,
		BaseURL:        server.URL,
		APIKey:         "k",
		WebhookSecret:  "s",
		RequestTimeout: 2 * time.Second,
	})
}

// --- Tests ---

func TestStartSessionCreatesUnknownSession(t *testing.T) {
	gateway := newWaha()
	client := newTestClient(t, gateway)

	if err := client.StartSession(context.Background(), "pix_owner"); err != nil {
		t.Fatalf("start unknown session: %v", err)
	}
	if gateway.creates != 1 || gateway.restarts != 0 {
		t.Fatalf("creates = %d restarts = %d, want 1 create", gateway.creates, gateway.restarts)
	}
}

func TestStartSessionRestoresStoppedSession(t *testing.T) {
	gateway := newWaha("pix_owner")
	gateway.states["pix_owner"] = []wahaState{{Status: StatusStopped}, {Status: StatusScanQRCode}}
	client := newTestClient(t, gateway)

	if err := client.StartSession(context.Background(), "pix_owner"); err != nil {
		t.Fatalf("start stopped session: %v", err)
	}
	if gateway.restarts != 1 || gateway.creates != 0 {
		t.Fatalf("restarts = %d creates = %d, want 1 restart", gateway.restarts, gateway.creates)
	}
	status, err := client.Status(context.Background(), "pix_owner")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.CanServeQR() {
		t.Fatalf("session should be scannable now, got %+v", status)
	}
}

func TestStartSessionLeavesPairedSessionAlone(t *testing.T) {
	gateway := newWaha("pix_owner")
	client := newTestClient(t, gateway)

	if err := client.StartSession(context.Background(), "pix_owner"); err != nil {
		t.Fatalf("start paired session: %v", err)
	}
	if gateway.creates != 0 || gateway.restarts != 0 {
		t.Fatalf("a restart would log the number out: creates = %d restarts = %d", gateway.creates, gateway.restarts)
	}
}

func TestQRRestartsSessionThenReturnsPNG(t *testing.T) {
	gateway := newWaha("pix_owner")
	gateway.states["pix_owner"] = []wahaState{{Status: StatusStopped}}
	client := newTestClient(t, gateway)

	if err := client.StartSession(context.Background(), "pix_owner"); err != nil {
		t.Fatalf("start: %v", err)
	}
	gateway.states["pix_owner"] = []wahaState{{Status: StatusScanQRCode}}

	png, err := client.QR(context.Background(), "pix_owner")
	if err != nil {
		t.Fatalf("qr: %v", err)
	}
	if !bytes.HasPrefix(png, qrPNG) {
		t.Fatalf("png = %q, want the PNG bytes", png[:12])
	}
}

func TestQRErrCarriesWahasExplanation(t *testing.T) {
	gateway := newWaha("pix_owner")
	gateway.states["pix_owner"] = []wahaState{{Status: StatusStopped}}
	client := newTestClient(t, gateway)

	_, err := client.QR(context.Background(), "pix_owner")
	if err == nil {
		t.Fatal("qr on a stopped session should fail")
	}
	if !strings.Contains(err.Error(), "Session status is not as expected") {
		t.Fatalf("err = %q, want WAHA's own explanation", err.Error())
	}
}

func TestStartSessionDoesNotRestartTwiceInCooldown(t *testing.T) {
	gateway := newWaha("pix_owner")
	gateway.states["pix_owner"] = []wahaState{{Status: StatusStopped}}
	client := newTestClient(t, gateway)

	for i := 0; i < 3; i++ {
		if err := client.StartSession(context.Background(), "pix_owner"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if gateway.restarts != 1 {
		t.Fatalf("restarts = %d, want 1: WAHA cannot be churned by the QR poll", gateway.restarts)
	}
}

// TestQRAsksWAHAForTheBinaryAnswer locks the header: with a JSON Accept WAHA
// answers with {"mimetype":...,"data":"base64"}, which no <img> can render.
func TestQRAsksWAHAForTheBinaryAnswer(t *testing.T) {
	gateway := newWaha("pix_owner")
	gateway.states["pix_owner"] = []wahaState{{Status: StatusScanQRCode}}
	client := newTestClient(t, gateway)

	if _, err := client.QR(context.Background(), "pix_owner"); err != nil {
		t.Fatalf("qr: %v", err)
	}
	if !strings.Contains(gateway.lastQRAccept, "image/png") {
		t.Fatalf("Accept = %q, want image/png", gateway.lastQRAccept)
	}
}

func TestQRUnwrapsTheBase64Envelope(t *testing.T) {
	gateway := newWaha("pix_owner")
	gateway.states["pix_owner"] = []wahaState{{Status: StatusScanQRCode}}
	gateway.qrAlwaysJSON = true
	client := newTestClient(t, gateway)

	png, err := client.QR(context.Background(), "pix_owner")
	if err != nil {
		t.Fatalf("qr: %v", err)
	}
	if !bytes.HasPrefix(png, qrPNG) {
		t.Fatalf("png = %q, want the PNG bytes", png[:12])
	}
}

func TestContactResolvesName(t *testing.T) {
	gateway := newWaha("pix_owner")
	gateway.contacts["37048606048491@lid"] = `{"id":"37048606048491@lid","name":"","pushname":"Thiago Henrique"}`
	client := newTestClient(t, gateway)

	got, err := client.Contact(context.Background(), "pix_owner", "37048606048491@lid")
	if err != nil {
		t.Fatalf("contact: %v", err)
	}
	if got.DisplayName() != "Thiago Henrique" {
		t.Fatalf("DisplayName = %q, want the pushname", got.DisplayName())
	}
}

func TestContactUnknownIsNotAnError(t *testing.T) {
	gateway := newWaha("pix_owner")
	client := newTestClient(t, gateway)

	got, err := client.Contact(context.Background(), "pix_owner", "5511999999999@c.us")
	if err != nil {
		t.Fatalf("unknown contact must not error: %v", err)
	}
	if got.DisplayName() != "" {
		t.Fatalf("expected empty name, got %q", got.DisplayName())
	}
}
