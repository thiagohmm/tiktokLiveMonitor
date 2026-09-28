package view

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/whatsapp"
)

// fakePixStore is an in-memory media.Store for webhook tests.
type fakePixStore struct{ objects map[string][]byte }

func newFakePixStore() *fakePixStore                       { return &fakePixStore{objects: map[string][]byte{}} }
func (s *fakePixStore) EnsureBucket(context.Context) error { return nil }
func (s *fakePixStore) Put(_ context.Context, key string, data []byte, _ string) error {
	s.objects[key] = data
	return nil
}
func (s *fakePixStore) Get(_ context.Context, key string) ([]byte, error) {
	return s.objects[key], nil
}
func (s *fakePixStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func signPix(secret, body string) string {
	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func setupPixWebhook(t *testing.T) (*HTTPServer, model.Repository) {
	t.Helper()
	srv, repo, _, _ := setupTestServer(t)
	cfg := whatsapp.Config{
		Enabled:       true,
		BaseURL:       "http://waha.invalid",
		APIKey:        "key",
		WebhookSecret: "secret",
	}
	srv.controller.SetPixQueueService(controller.NewPixQueueService(repo, whatsapp.NewClient(cfg), newFakePixStore()))
	return srv, repo
}

func TestWhatsAppWebhookRejectsInvalidSignature(t *testing.T) {
	srv, _ := setupPixWebhook(t)
	body := `{"event":"message","session":"pix_owner","payload":{"from":"5511@c.us","body":"oi"}}`

	cases := []struct {
		name   string
		header string
	}{
		{"missing", ""},
		{"wrong", "deadbeef"},
		{"tampered", signPix("secret", `{"event":"other"}`)},
	}
	for _, c := range cases {
		req := newOrgRequest(http.MethodPost, "/api/webhooks/whatsapp", bytes.NewBufferString(body))
		if c.header != "" {
			req.Header.Set("X-Webhook-Hmac", c.header)
		}
		rec := httptest.NewRecorder()
		srv.handleWhatsAppWebhook(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", c.name, rec.Code)
		}
	}
}

func TestWhatsAppWebhookStoresInboundText(t *testing.T) {
	srv, repo := setupPixWebhook(t)
	// The webhook resolves owner by session name, so the pairing row must exist.
	if _, err := repo.UpsertPixSession("owner-1", "pix_owner", model.PixSessionConnected, "5511999990000", "5511999990000@c.us", nil); err != nil {
		t.Fatalf("seed pix session: %v", err)
	}

	body := `{"event":"message","session":"pix_owner","me":{"id":"5511999990000@c.us"},
		"payload":{"id":"m-1","from":"5511888887777@c.us","body":"segue comprovante","timestamp":1667561485}}`
	req := newOrgRequest(http.MethodPost, "/api/webhooks/whatsapp", bytes.NewBufferString(body))
	req.Header.Set("X-Webhook-Hmac", signPix("secret", body))
	rec := httptest.NewRecorder()
	srv.handleWhatsAppWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	tickets, err := repo.ListPixTickets("owner-1", model.PixTicketPending, 10)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("expected 1 pending ticket, got %d err=%v", len(tickets), err)
	}
	messages, err := repo.ListPixMessages("owner-1", tickets[0].ID, 10)
	if err != nil || len(messages) != 1 || messages[0].Body != "segue comprovante" {
		t.Fatalf("unexpected messages: %+v err=%v", messages, err)
	}

	// A duplicate delivery (same WhatsApp id) must not create a second message.
	req = newOrgRequest(http.MethodPost, "/api/webhooks/whatsapp", bytes.NewBufferString(body))
	req.Header.Set("X-Webhook-Hmac", signPix("secret", body))
	rec = httptest.NewRecorder()
	srv.handleWhatsAppWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate: expected 200, got %d", rec.Code)
	}
	messages, _ = repo.ListPixMessages("owner-1", tickets[0].ID, 10)
	if len(messages) != 1 {
		t.Fatalf("duplicate created extra message: %d", len(messages))
	}
}

func TestPixValuesEndpointRoundTrip(t *testing.T) {
	srv, _ := setupPixWebhook(t)

	// Fresh owner: empty list.
	rec := httptest.NewRecorder()
	srv.handlePixValues(rec, newOrgRequest(http.MethodGet, "/api/pix/values", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var initial struct {
		Values []int64 `json:"values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &initial); err != nil {
		t.Fatalf("GET decode: %v", err)
	}
	if len(initial.Values) != 0 {
		t.Fatalf("expected no values, got %v", initial.Values)
	}

	// PUT accepts pt-BR strings, dedupes, and stores cents.
	rec = httptest.NewRecorder()
	body := `{"values":["15,00","10","R$ 1.234,56","15,00",""]}`
	srv.handlePixValues(rec, newOrgRequest(http.MethodPut, "/api/pix/values", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.handlePixValues(rec, newOrgRequest(http.MethodGet, "/api/pix/values", nil))
	var got struct {
		Values []int64 `json:"values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET decode: %v", err)
	}
	want := []int64{1000, 1500, 123456}
	if len(got.Values) != len(want) {
		t.Fatalf("values = %v, want %v", got.Values, want)
	}
	for i := range want {
		if got.Values[i] != want[i] {
			t.Fatalf("values = %v, want %v", got.Values, want)
		}
	}

	// Garbage input is a client error.
	rec = httptest.NewRecorder()
	srv.handlePixValues(rec, newOrgRequest(http.MethodPut, "/api/pix/values", strings.NewReader(`{"values":["abc"]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid value: expected 400, got %d", rec.Code)
	}
}
