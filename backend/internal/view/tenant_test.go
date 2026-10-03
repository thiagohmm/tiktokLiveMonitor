package view

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/tenant"
)

// newOrgRequest builds a request already resolved (as the tenant middleware
// would) to the owner of the harness organization, for tests that call a
// handler directly.
func newOrgRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	return req.WithContext(tenant.With(req.Context(), tenant.Tenant{
		OrgID: testOrg, UserID: "test-owner", Role: model.OrgRoleOwner,
	}))
}

// ---- SSE ----

func TestPublishSSEDeliversOnlyToTheEventOrganization(t *testing.T) {
	srv := &HTTPServer{sseClients: make(map[*sseClient]struct{})}
	a := newSSEClient("org-a", nil, nil)
	b := newSSEClient("org-b", nil, nil)
	srv.sseClients[a] = struct{}{}
	srv.sseClients[b] = struct{}{}

	srv.publishSSE("org-b", "only-b", map[string]string{"x": "1"})
	srv.publishSSE("", "no-org", map[string]string{"x": "2"})

	if len(a.ch) != 0 {
		t.Fatalf("client of org-a received %d events of another organization", len(a.ch))
	}
	if len(b.ch) != 1 {
		t.Fatalf("client of org-b: expected 1 event, got %d", len(b.ch))
	}
	if msg := string(<-b.ch); !strings.HasPrefix(msg, "event: only-b\n") {
		t.Fatalf("client of org-b got unexpected message %q", msg)
	}
}

// sseStream connects an SSE client of orgID and returns the event names it receives.
func sseStream(t *testing.T, base, orgID string) <-chan string {
	t.Helper()
	resp, err := http.Get(base + "/events?org=" + orgID)
	if err != nil {
		t.Fatalf("SSE connect %s: %v", orgID, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	events := make(chan string, 64)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if name, ok := strings.CutPrefix(scanner.Text(), "event: "); ok {
				events <- name
			}
		}
	}()
	return events
}

func nextSSEEvent(t *testing.T, events <-chan string, who string) string {
	t.Helper()
	select {
	case e, ok := <-events:
		if !ok {
			t.Fatalf("%s: SSE stream closed", who)
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: timed out waiting for an SSE event", who)
	}
	return ""
}

func TestSSEClientsAreIsolatedByOrganization(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)
	// The org comes from the query only in this test harness; in production
	// the tenant middleware resolves it from the authenticated user.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := r.URL.Query().Get("org")
		srv.handleSSE(w, r.WithContext(tenant.With(r.Context(), tenant.Tenant{OrgID: org, UserID: "u-" + org})))
	}))
	t.Cleanup(ts.Close)

	orgA, orgB := testOrg, "org-b"
	streamA := sseStream(t, ts.URL, orgA)
	streamB := sseStream(t, ts.URL, orgB)
	// server-state is written after the client is registered.
	for who, s := range map[string]<-chan string{"A": streamA, "B": streamB} {
		if e := nextSSEEvent(t, s, who); e != "server-state" {
			t.Fatalf("%s: expected server-state first, got %q", who, e)
		}
	}

	// Each client's queue is FIFO: had A received only-b or no-org, it would
	// show up before its own marker.
	srv.publishSSE(orgB, "only-b", map[string]string{})
	srv.publishSSE("", "no-org", map[string]string{})
	srv.publishSSE(orgA, "only-a", map[string]string{})

	if e := nextSSEEvent(t, streamA, "A"); e != "only-a" {
		t.Fatalf("A: expected only-a, got %q (leak from another organization)", e)
	}
	if e := nextSSEEvent(t, streamB, "B"); e != "only-b" {
		t.Fatalf("B: expected only-b, got %q", e)
	}
	srv.publishSSE(orgB, "marker-b", map[string]string{})
	if e := nextSSEEvent(t, streamB, "B"); e != "marker-b" {
		t.Fatalf("B: expected marker-b, got %q (leak from another organization)", e)
	}
}

// ---- Auth-enabled harness ----

// tenantFixture is a server with auth enabled (local sessions) and two
// customer organizations: A (with live1) and B (with liveB). The legacy
// organization (testOrg) accepts no members, so A is a real organization.
type tenantFixture struct {
	srv       *HTTPServer
	repo      model.Repository
	orgA      string
	sessionA  model.LiveSession
	orgB      string
	sessionB  model.LiveSession
	ownerA    string
	operatorA string
	ownerB    string
	stranger  string
	admin     string
}

func setupTenantFixture(t *testing.T) *tenantFixture {
	t.Helper()
	srv, repo, _, _ := setupTestServer(t)
	srv.auth.Enabled = true
	srv.admin = nil

	orgA, err := repo.CreateOrganization("Org A", 3)
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := repo.CreateOrganization("Org B", 3)
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}
	f := &tenantFixture{
		srv: srv, repo: repo, orgA: orgA.ID, orgB: orgB.ID,
		ownerA: "owner-a", operatorA: "operator-a", ownerB: "owner-b",
		stranger: "stranger", admin: "platform-admin",
	}
	for _, m := range []struct{ org, user, role string }{
		{f.orgA, f.ownerA, model.OrgRoleOwner},
		{f.orgA, f.operatorA, model.OrgRoleOperator},
		// The platform admin is an operator of A: it manages A without
		// being one of its owners.
		{f.orgA, f.admin, model.OrgRoleOperator},
		{f.orgB, f.ownerB, model.OrgRoleOwner},
	} {
		if _, err := repo.UpsertOrgMember(m.org, m.user, m.user+"@example.com", m.role); err != nil {
			t.Fatalf("add member %s: %v", m.user, err)
		}
	}
	f.sessionA, err = repo.BeginLiveSession(f.orgA, "live1", time.Now())
	if err != nil {
		t.Fatalf("begin session of org A: %v", err)
	}
	f.sessionB, err = repo.BeginLiveSession(f.orgB, "liveB", time.Now())
	if err != nil {
		t.Fatalf("begin session of org B: %v", err)
	}
	return f
}

func (f *tenantFixture) refA() model.LiveRef {
	return model.LiveRef{ID: f.sessionA.ID, Name: "live1", OrgID: f.orgA}
}

func (f *tenantFixture) refB() model.LiveRef {
	return model.LiveRef{ID: f.sessionB.ID, Name: "liveB", OrgID: f.orgB}
}

// do runs handler behind the production auth + tenant middlewares as userID.
func (f *tenantFixture) do(t *testing.T, handler http.HandlerFunc, userID, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	role := "subscriber"
	if userID == f.admin {
		role = "admin"
	}
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	token := auth.RandomToken()
	csrf := auth.TokenHash("csrf:" + token)
	pool := f.srv.auth.Store.DB
	if _, err := pool.Exec(`INSERT INTO users(id,email,role,active) VALUES($1,$2,$3,true) ON CONFLICT(id) DO UPDATE SET role=excluded.role`, userID, userID+"@example.com", role); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO auth_sessions(token_hash,user_id,csrf_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')`, auth.TokenHash(token), userID, auth.TokenHash(csrf)); err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: auth.AccessTokenCookie, Value: token})
	req.Header.Set("X-CSRF-Token", csrf)
	rec := httptest.NewRecorder()
	f.srv.auth.Middleware(f.srv.tenantMiddleware(handler)).ServeHTTP(rec, req)
	return rec
}

func expectErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("expected %d, got %d body=%s", status, rec.Code, rec.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if payload["code"] != code || payload["error"] == "" {
		t.Fatalf("expected code %q with a message, got %+v", code, payload)
	}
}

// ---- Tenant resolution ----

func TestTenantUserWithoutOrganizationIsForbidden(t *testing.T) {
	f := setupTenantFixture(t)

	rec := f.do(t, f.srv.handleState, f.stranger, http.MethodGet, "/api/state", "")
	expectErrorCode(t, rec, http.StatusForbidden, errCodeNoOrg)

	// A platform admin without membership operates the legacy organization.
	if _, err := f.repo.DeleteOrgMember(f.orgA, f.admin); err != nil {
		t.Fatalf("drop admin membership: %v", err)
	}
	f.srv.tenants.invalidate(f.admin)
	if rec := f.do(t, f.srv.handleState, f.admin, http.MethodGet, "/api/state", ""); rec.Code != http.StatusOK {
		t.Fatalf("platform admin: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTenantDisabledOrganizationIsForbidden(t *testing.T) {
	f := setupTenantFixture(t)
	inactive := false
	if _, err := f.repo.UpdateOrganization(f.orgB, nil, nil, &inactive); err != nil {
		t.Fatalf("disable org B: %v", err)
	}

	rec := f.do(t, f.srv.handleState, f.ownerB, http.MethodGet, "/api/state", "")
	expectErrorCode(t, rec, http.StatusForbidden, errCodeOrgDisabled)

	if rec := f.do(t, f.srv.handleState, f.ownerA, http.MethodGet, "/api/state", ""); rec.Code != http.StatusOK {
		t.Fatalf("member of an active org: expected 200, got %d", rec.Code)
	}
}

// ---- Destructive operations ----

func TestTenantDestructiveOperationsRequireOwnerAndStayInOrg(t *testing.T) {
	f := setupTenantFixture(t)
	for _, ref := range []model.LiveRef{f.refA(), f.refA(), f.refB()} {
		if _, err := f.repo.AddGift(ref, "u1", "User", "Rosa", 1, 0); err != nil {
			t.Fatalf("seed gift: %v", err)
		}
		if err := f.repo.LogAnomaly(ref, "msg", true, "SPAM", "u1"); err != nil {
			t.Fatalf("seed anomaly: %v", err)
		}
	}

	if rec := f.do(t, f.srv.handleGifts, f.operatorA, http.MethodDelete, "/api/gifts", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("operator DELETE /api/gifts: expected 403, got %d", rec.Code)
	}
	if rec := f.do(t, f.srv.handleClearHistory, f.operatorA, http.MethodPost, "/api/clear-history", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("operator clear-history: expected 403, got %d", rec.Code)
	}
	if gifts, _ := f.repo.GetRecentGifts(f.orgA, "live1", 10); len(gifts) != 2 {
		t.Fatalf("operator must not delete gifts, %d left", len(gifts))
	}

	deleted := func(rec *httptest.ResponseRecorder) float64 {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("owner: expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		n, _ := payload["deleted"].(float64)
		return n
	}
	if n := deleted(f.do(t, f.srv.handleGifts, f.ownerA, http.MethodDelete, "/api/gifts", "")); n != 2 {
		t.Fatalf("owner DELETE /api/gifts: expected 2 deleted, got %v", n)
	}
	if n := deleted(f.do(t, f.srv.handleClearHistory, f.ownerA, http.MethodPost, "/api/clear-history", "")); n != 2 {
		t.Fatalf("owner clear-history: expected 2 deleted, got %v", n)
	}

	if gifts, _ := f.repo.GetRecentGifts(f.orgA, "live1", 10); len(gifts) != 0 {
		t.Fatalf("org A gifts should be gone, %d left", len(gifts))
	}
	if gifts, err := f.repo.GetRecentGifts(f.orgB, "liveB", 10); err != nil || len(gifts) != 1 {
		t.Fatalf("org B gifts must survive, got %d err=%v", len(gifts), err)
	}
	if logs, err := f.repo.GetRecentModerations(f.orgB, 10); err != nil || len(logs) != 1 {
		t.Fatalf("org B moderation must survive, got %d err=%v", len(logs), err)
	}
}

func TestTenantTargetGiftAnswerOfAnotherOrgIsNotFound(t *testing.T) {
	f := setupTenantFixture(t)
	id, err := f.repo.AddTargetGiftHistory(f.refB(), "u1", "User", "Rosa", time.Now(), false)
	if err != nil {
		t.Fatalf("seed target gift: %v", err)
	}
	body := `{"id":` + strconv.FormatInt(id, 10) + `,"responseType":"manual"}`

	if rec := f.do(t, f.srv.handleTargetGiftHistoryAnswer, f.ownerA, http.MethodPost, "/api/target-gift-history/answer", body); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org answer: expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
	if pending, _ := f.repo.GetPendingTargetGiftHistory(f.orgB, "liveB", 10); len(pending) != 1 {
		t.Fatalf("cross-org answer must not touch the entry, pending=%d", len(pending))
	}
	if rec := f.do(t, f.srv.handleTargetGiftHistoryAnswer, f.ownerB, http.MethodPost, "/api/target-gift-history/answer", body); rec.Code != http.StatusOK {
		t.Fatalf("own-org answer: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---- Organization members ----

func TestOrgMembersDeleteIsScopedToTheOrganization(t *testing.T) {
	f := setupTenantFixture(t)
	del := func(caller, userID string) *httptest.ResponseRecorder {
		return f.do(t, f.srv.handleOrgMembersDelete, caller, http.MethodPost, "/api/org/members/delete?userId="+userID, "")
	}

	if rec := del(f.ownerA, f.ownerB); rec.Code != http.StatusNotFound {
		t.Fatalf("delete member of another org: expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
	if m, err := f.repo.GetMembership(f.ownerB); err != nil || m.OrgID != f.orgB {
		t.Fatalf("member of org B must survive: %+v err=%v", m, err)
	}

	if rec := del(f.operatorA, f.ownerA); rec.Code != http.StatusForbidden {
		t.Fatalf("operator deleting a member: expected 403, got %d", rec.Code)
	}

	if rec := del(f.ownerA, f.ownerA); rec.Code != http.StatusForbidden {
		t.Fatalf("owner removing self: expected 403, got %d", rec.Code)
	}

	// The platform admin manages org A but must keep one owner.
	if rec := del(f.admin, f.ownerA); rec.Code != http.StatusConflict {
		t.Fatalf("removing the last owner: expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	demote := `{"userId":"` + f.ownerA + `","role":"operator"}`
	if rec := f.do(t, f.srv.handleOrgMembersUpdate, f.admin, http.MethodPatch, "/api/org/members/update", demote); rec.Code != http.StatusConflict {
		t.Fatalf("demoting the last owner: expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, f.srv.handleOrgMembersUpdate, f.ownerA, http.MethodPatch, "/api/org/members/update", demote); rec.Code != http.StatusForbidden {
		t.Fatalf("owner changing own role: expected 403, got %d", rec.Code)
	}

	if rec := del(f.ownerA, f.operatorA); rec.Code != http.StatusOK {
		t.Fatalf("owner deleting own operator: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := f.repo.GetMembership(f.operatorA); err != model.ErrOrgNotFound {
		t.Fatalf("operator membership should be gone, got %v", err)
	}
	// The removed operator loses access right away (cache invalidated).
	rec := f.do(t, f.srv.handleState, f.operatorA, http.MethodGet, "/api/state", "")
	expectErrorCode(t, rec, http.StatusForbidden, errCodeNoOrg)
}

// ---- Fila PIX: WhatsApp pairing is owner-only ----

func TestPixWhatsAppPairingIsOwnerOnly(t *testing.T) {
	f := setupTenantFixture(t)
	pairing := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
	}{
		{"connect", f.srv.handlePixWhatsAppConnect, http.MethodPost, "/api/pix/whatsapp/connect"},
		{"qr", f.srv.handlePixWhatsAppQR, http.MethodGet, "/api/pix/whatsapp/qr"},
		{"disconnect", f.srv.handlePixWhatsAppDisconnect, http.MethodPost, "/api/pix/whatsapp/disconnect"},
	}
	for _, p := range pairing {
		if rec := f.do(t, p.handler, f.operatorA, p.method, p.target, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("operator %s: expected 403, got %d body=%s", p.name, rec.Code, rec.Body.String())
		}
		// The owner passes the role check (the queue itself is disabled
		// in this harness, hence 503).
		if rec := f.do(t, p.handler, f.ownerA, p.method, p.target, ""); rec.Code == http.StatusForbidden {
			t.Fatalf("owner %s: must not be forbidden, got %d body=%s", p.name, rec.Code, rec.Body.String())
		}
	}
	// Operators still work the queue: status is readable.
	if rec := f.do(t, f.srv.handlePixWhatsAppStatus, f.operatorA, http.MethodGet, "/api/pix/whatsapp/status", ""); rec.Code == http.StatusForbidden {
		t.Fatalf("operator status: must not be forbidden, got %d", rec.Code)
	}
	if rec := f.do(t, f.srv.handlePixTickets, f.operatorA, http.MethodGet, "/api/pix/tickets", ""); rec.Code == http.StatusForbidden {
		t.Fatalf("operator tickets: must not be forbidden, got %d", rec.Code)
	}
}
