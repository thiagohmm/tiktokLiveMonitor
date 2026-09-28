// Package whatsapp is a small client for the WAHA HTTP API (WhatsApp gateway)
// plus the webhook parsing/signature helpers used by the Fila PIX.
//
// WAHA speaks the WhatsApp Web multi-device protocol, so any phone pairs via QR
// code. The client is intentionally thin: it exposes exactly the operations the
// Fila PIX needs (session lifecycle, text send and inbound media download).
package whatsapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultMaxMediaBytes bounds inbound receipt downloads (16 MB).
const DefaultMaxMediaBytes int64 = 16 * 1024 * 1024

// Config holds the WAHA connection settings (env-driven).
type Config struct {
	Enabled        bool
	BaseURL        string
	APIKey         string
	WebhookSecret  string
	MaxMediaBytes  int64
	RequestTimeout time.Duration
}

// LoadConfigFromEnv builds the WAHA config. WAHA_ENABLED=0 disables the feature
// without taking the backend down.
func LoadConfigFromEnv() Config {
	enabled := strings.TrimSpace(os.Getenv("WAHA_ENABLED")) != "0"
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("WAHA_URL")), "/")
	if base == "" {
		base = "http://localhost:3000"
	}
	maxBytes := DefaultMaxMediaBytes
	if v := strings.TrimSpace(os.Getenv("PIX_MEDIA_MAX_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBytes = n
		}
	}
	return Config{
		Enabled:        enabled,
		BaseURL:        base,
		APIKey:         strings.TrimSpace(os.Getenv("WAHA_API_KEY")),
		WebhookSecret:  strings.TrimSpace(os.Getenv("WAHA_WEBHOOK_SECRET")),
		MaxMediaBytes:  maxBytes,
		RequestTimeout: 60 * time.Second,
	}
}

// Configured reports whether the client has the minimum settings to operate.
func (c Config) Configured() bool {
	return c.Enabled && c.BaseURL != "" && c.APIKey != "" && c.WebhookSecret != ""
}

// SessionNameForOrg derives the deterministic WAHA session name for a user.
// The owner UUID is used without hyphens so the name is safe for URLs.
func SessionNameForOrg(orgID string) string {
	id := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(orgID), "-", ""))
	if id == "" {
		return ""
	}
	return "pix_" + id
}

// SessionStatus is the normalized WAHA session state.
type SessionStatus struct {
	Found     bool
	Status    string
	State     string
	MePhone   string
	MeJID     string
	Connected bool
}

// WAHA publishes a pairing QR only while the session waits in SCAN_QR_CODE.
// The predicates below tell the caller whether to wait, restart or create.
// The WAHA session statuses the pairing flow has to react to.
const (
	StatusScanQRCode = "SCAN_QR_CODE"
	StatusStarting   = "STARTING"
	StatusStopped    = "STOPPED"
	StatusFailed     = "FAILED"
)

// CanServeQR reports whether WAHA has a pairing QR ready to hand out.
func (s SessionStatus) CanServeQR() bool {
	return s.ReportedAs(StatusScanQRCode)
}

// NeedsRestart reports a session WAHA keeps parked: stopped (after a logout) or
// broken. Such a session never serves a QR until it starts again.
func (s SessionStatus) NeedsRestart() bool {
	return s.Found && !s.Connected && !s.CanServeQR() && s.ReportedAs(StatusStopped, StatusFailed)
}

// Failed reports a session WAHA gave up on, so the UI can ask for a retry.
func (s SessionStatus) Failed() bool {
	return s.ReportedAs(StatusFailed)
}

// Starting reports the short window where WAHA is booting the session: the QR
// is coming, so the caller waits instead of restarting a second time.
func (s SessionStatus) Starting() bool {
	return !s.Connected && !s.CanServeQR() && s.ReportedAs(StatusStarting)
}

// ReportedAs matches the session against WAHA's own status names, ignoring case
// and spaces. Both status and engine.state are consulted: either can carry it.
func (s SessionStatus) ReportedAs(states ...string) bool {
	for _, state := range states {
		if equalsState(s.Status, state) || equalsState(s.State, state) {
			return true
		}
	}
	return false
}

func equalsState(actual, wanted string) bool {
	return strings.EqualFold(strings.TrimSpace(actual), wanted)
}

// Client talks to one WAHA instance using the global API key.
type Client struct {
	cfg         Config
	http        *http.Client
	restartMu   sync.Mutex
	lastRestart map[string]time.Time
}

// restartCooldown bounds how often a session may be restarted: the browser polls
// the QR every few seconds, and WAHA that cannot pair must not be churned.
const restartCooldown = 10 * time.Second

// NewClient creates a WAHA client.
func NewClient(cfg Config) *Client {
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	c := &Client{cfg: cfg, lastRestart: map[string]time.Time{}}
	c.http = &http.Client{Timeout: timeout, CheckRedirect: c.sameHostRedirect}
	return c
}

// sameHostRedirect refuses redirects that leave the WAHA host: Go forwards
// custom headers such as X-Api-Key on redirects, so following one to another
// host would leak the key.
func (c *Client) sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("WAHA: too many redirects")
	}
	if !c.isWAHAHost(req.URL) {
		return fmt.Errorf("WAHA: redirect to foreign host refused")
	}
	return nil
}

// isWAHAHost reports whether u points at the configured WAHA_URL host
// (scheme + host + port).
func (c *Client) isWAHAHost(u *url.URL) bool {
	base, err := url.Parse(c.cfg.BaseURL)
	if err != nil || base.Host == "" || u == nil {
		return false
	}
	return strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
}

// Config exposes the client settings.
func (c *Client) Config() Config { return c.cfg }

// StartSession puts the session into a live state: it creates the session when
// WAHA does not know it and restarts one WAHA left stopped or failed. A session
// that is already starting or paired is left alone, because restarting a paired
// session would log the number out.
func (c *Client) StartSession(ctx context.Context, session string) error {
	if strings.TrimSpace(session) == "" {
		return errors.New("session name is required")
	}
	status, err := c.Status(ctx, session)
	if err != nil {
		return err
	}
	switch {
	case !status.Found:
		return c.createSession(ctx, session)
	case status.NeedsRestart() && !c.restartedRecently(session):
		c.markRestart(session)
		return c.restartSession(ctx, session)
	default:
		return nil
	}
}

// restartedRecently reports whether a restart is still cooling down.
func (c *Client) restartedRecently(session string) bool {
	c.restartMu.Lock()
	defer c.restartMu.Unlock()
	last, ok := c.lastRestart[session]
	return ok && time.Since(last) < restartCooldown
}

func (c *Client) markRestart(session string) {
	c.restartMu.Lock()
	defer c.restartMu.Unlock()
	c.lastRestart[session] = time.Now()
}

// createSession registers and starts the session. A duplicate answer (WAHA
// replies 422 "already exists") is not an error: the caller reads the state from
// the status it queries next.
func (c *Client) createSession(ctx context.Context, session string) error {
	body, _ := json.Marshal(map[string]any{"name": session, "start": true})
	res, err := c.do(ctx, http.MethodPost, "/api/sessions/", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer closeBody(res)
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	switch res.StatusCode {
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
		return nil
	default:
		return wahaError("create session", res)
	}
}

// restartSession starts a stopped or failed session, which is the only way back
// to SCAN_QR_CODE (and therefore to a pairing QR).
func (c *Client) restartSession(ctx context.Context, session string) error {
	res, err := c.do(ctx, http.MethodPost, "/api/sessions/"+session+"/restart", nil, "")
	if err != nil {
		return err
	}
	defer closeBody(res)
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	return wahaError("restart session", res)
}

// QR returns the pairing QR PNG for the session.
func (c *Client) QR(ctx context.Context, session string) ([]byte, error) {
	res, err := c.doFile(ctx, http.MethodGet, "/api/"+session+"/auth/qr")
	if err != nil {
		return nil, err
	}
	defer closeBody(res)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, wahaError("qr", res)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("WAHA qr read: %w", err)
	}
	return decodeFileBody(data)
}

// Status reads the session state from WAHA.
func (c *Client) Status(ctx context.Context, session string) (SessionStatus, error) {
	res, err := c.do(ctx, http.MethodGet, "/api/sessions/"+session, nil, "")
	if err != nil {
		return SessionStatus{}, err
	}
	defer closeBody(res)
	if res.StatusCode == http.StatusNotFound {
		return SessionStatus{Found: false}, nil
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		var raw struct {
			Status string `json:"status"`
			Engine struct {
				State string `json:"state"`
			} `json:"engine"`
			Me struct {
				ID    string `json:"id"`
				Phone string `json:"phone"`
			} `json:"me"`
		}
		if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
			return SessionStatus{}, fmt.Errorf("WAHA status: invalid json: %w", err)
		}
		status := SessionStatus{
			Found:   true,
			Status:  strings.ToUpper(strings.TrimSpace(raw.Status)),
			State:   strings.ToUpper(strings.TrimSpace(raw.Engine.State)),
			MePhone: strings.TrimSpace(raw.Me.Phone),
			MeJID:   strings.TrimSpace(raw.Me.ID),
		}
		status.Connected = status.ReportedAs("CONNECTED", "WORKING") || status.MePhone != ""
		return status, nil
	}
	return SessionStatus{}, wahaError("status", res)
}

// Logout unpairs the session. WAHA 4xx/5xx completion still means the local state
// can be reset, so only transport errors are returned.
func (c *Client) Logout(ctx context.Context, session string) error {
	res, err := c.do(ctx, http.MethodPost, "/api/sessions/"+session+"/logout", nil, "")
	if err != nil {
		return err
	}
	defer closeBody(res)
	if res.StatusCode >= 500 {
		return fmt.Errorf("WAHA logout: HTTP %d", res.StatusCode)
	}
	return nil
}

// SendText sends a plain text message and returns the WAHA message id when one
// is present. A 2xx without an id still counts as sent: losing the local row
// would make the operator resend the same message.
func (c *Client) SendText(ctx context.Context, session, chatID, text string) (string, error) {
	body, _ := json.Marshal(map[string]any{"session": session, "chatId": chatID, "text": text})
	res, err := c.do(ctx, http.MethodPost, "/api/sendText", bytes.NewReader(body), "application/json")
	if err != nil {
		return "", err
	}
	defer closeBody(res)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("WAHA sendText: HTTP %d", res.StatusCode)
	}
	var payload any
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&payload); err != nil {
		return "", nil
	}
	if id := extractMessageID(payload); id != "" {
		return id, nil
	}
	return "", nil
}

// Contact is the address-book/profile info WAHA knows about a chat destination.
// For privacy-restricted senders (WhatsApp's @lid) the real phone is never
// present — only the profile name is available.
type Contact struct {
	ID       string
	Name     string // saved contact name (operator's address book)
	Pushname string // WhatsApp profile name
}

// DisplayName prefers the saved name, falling back to the profile pushname.
func (c Contact) DisplayName() string {
	if n := strings.TrimSpace(c.Name); n != "" {
		return n
	}
	return strings.TrimSpace(c.Pushname)
}

// Contact resolves a chat destination (phone@c.us or local@lid) into its known
// name via WAHA's contacts endpoint. An unknown contact is not an error.
func (c *Client) Contact(ctx context.Context, session, chatID string) (Contact, error) {
	if strings.TrimSpace(chatID) == "" {
		return Contact{}, fmt.Errorf("chat id is required")
	}
	res, err := c.do(ctx, http.MethodGet, "/api/"+session+"/contacts/"+url.PathEscape(chatID), nil, "")
	if err != nil {
		return Contact{}, err
	}
	defer closeBody(res)
	if res.StatusCode == http.StatusNotFound {
		return Contact{}, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Contact{}, wahaError("contact", res)
	}
	var raw struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Pushname string `json:"pushname"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&raw); err != nil {
		return Contact{}, fmt.Errorf("WAHA contact: invalid json: %w", err)
	}
	return Contact{ID: raw.ID, Name: raw.Name, Pushname: raw.Pushname}, nil
}

// DownloadMedia fetches the absolute media URL delivered by the webhook.
func (c *Client) DownloadMedia(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("WAHA media url invalid")
	}
	// The URL comes from the webhook payload: only the WAHA_URL host may be
	// contacted, otherwise X-Api-Key would be sent to an arbitrary server
	// (and the backend could be used to reach internal services).
	if !c.isWAHAHost(u) {
		return nil, fmt.Errorf("WAHA media url host %q is not WAHA_URL", u.Host)
	}
	res, err := c.doFile(ctx, http.MethodGet, u.String())
	if err != nil {
		return nil, err
	}
	defer closeBody(res)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("WAHA media: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, c.cfg.MaxMediaBytes+1))
	if err != nil {
		return nil, fmt.Errorf("WAHA media read: %w", err)
	}
	data, err = decodeFileBody(data)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > c.cfg.MaxMediaBytes {
		return nil, fmt.Errorf("WAHA media exceeds %d bytes", c.cfg.MaxMediaBytes)
	}
	return data, nil
}

// What WAHA should answer with. WAHA replies with the bytes for an image/binary
// Accept and with {"mimetype":...,"data":"<base64>"} for a JSON Accept, so the
// header decides what the caller has to unwrap.
const (
	acceptJSON = "application/json"
	acceptFile = "image/png, application/octet-stream"
)

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	return c.send(ctx, method, path, body, contentType, acceptJSON)
}

// doFile fetches an endpoint whose answer is a file (QR PNG, inbound media).
func (c *Client) doFile(ctx context.Context, method, path string) (*http.Response, error) {
	return c.send(ctx, method, path, nil, "", acceptFile)
}

func (c *Client) send(ctx context.Context, method, path string, body io.Reader, contentType, accept string) (*http.Response, error) {
	url := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		url = c.cfg.BaseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.cfg.APIKey)
	req.Header.Set("Accept", accept)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("WAHA request: %w", err)
	}
	return res, nil
}

// decodeFileBody unwraps the base64 envelope WAHA uses when it answers with
// JSON, passing through anything that is already the raw file. A data: URL prefix
// is tolerated.
func decodeFileBody(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return data, nil
	}
	var envelope struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil || envelope.Data == "" {
		return data, nil
	}
	encoded := envelope.Data
	if idx := strings.Index(encoded, ","); idx >= 0 && strings.HasPrefix(encoded, "data:") {
		encoded = encoded[idx+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("WAHA file body: invalid base64: %w", err)
	}
	return raw, nil
}

func closeBody(res *http.Response) {
	if res != nil && res.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		_ = res.Body.Close()
	}
}

// idContainers lists the known WAHA response shapes for the message id.
var idContainers = []string{"", "_data", "key", "data"}

func extractMessageID(payload any) string {
	for _, container := range idContainers {
		node := payload
		if container != "" {
			m, ok := payload.(map[string]any)
			if !ok {
				continue
			}
			node = m[container]
		}
		m, ok := node.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m["id"].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// wahaError keeps WAHA's own explanation in the error. "Session status is not
// as expected" (a parked session) and "connection refused" (a dead gateway) need
// different answers, so a bare HTTP code is not enough to diagnose a click.
func wahaError(what string, res *http.Response) error {
	detail := readWahaMessage(res.Body)
	if detail == "" {
		return fmt.Errorf("WAHA %s: HTTP %d", what, res.StatusCode)
	}
	return fmt.Errorf("WAHA %s: HTTP %d: %s", what, res.StatusCode, detail)
}

func readWahaMessage(body io.Reader) string {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	data, err := io.ReadAll(io.LimitReader(body, 1<<16))
	if err != nil {
		return ""
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Error)
}

// ChatID converts an E.164-ish destination into WAHA's chatId. Values that
// already carry a JID domain pass through.
func ChatID(to string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", errors.New("destination is required")
	}
	if strings.Contains(to, "@") {
		local := to[:strings.Index(to, "@")]
		if !strings.ContainsAny(local, "0123456789") {
			return "", fmt.Errorf("destination JID invalid")
		}
		return to, nil
	}
	var b strings.Builder
	for _, r := range to {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if digits == "" {
		return "", fmt.Errorf("destination number invalid")
	}
	return digits + "@c.us", nil
}
