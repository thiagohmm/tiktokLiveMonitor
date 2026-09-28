package controller

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/media"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/receipt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/whatsapp"
)

func TestMonitorAttachmentStore(t *testing.T) {
	st := NewMonitorAttachmentStore()
	if got := st.Attach("org-a", "user-a", "live1"); got != 1 {
		t.Fatalf("first attach watchers = %d", got)
	}
	if got := st.Attach("org-a", "user-b", "live1"); got != 2 {
		t.Fatalf("second attach watchers = %d", got)
	}
	if got := st.Detach("org-a", "user-a", "live1"); got != 1 {
		t.Fatalf("detach user-a remaining = %d", got)
	}
	// Detaching a user that is not attached leaves the count untouched.
	if got := st.Detach("org-a", "user-a", "live1"); got != 1 {
		t.Fatalf("detach twice remaining = %d", got)
	}
	remaining := st.DetachAll("org-a", "user-b")
	if remaining["live1"] != 0 {
		t.Fatalf("detach all remaining = %v", remaining)
	}
	if got := st.Watchers("org-a", "live1"); got != 0 {
		t.Fatalf("watchers after all left = %d", got)
	}
	if got := st.Attach("", "user-a", "live1"); got != 0 {
		t.Fatalf("attach without organization must be ignored, got %d", got)
	}
}

func TestMonitorAttachmentStoreIsolatesOrgs(t *testing.T) {
	st := NewMonitorAttachmentStore()
	if got := st.Attach("org-a", "user-a", "live1"); got != 1 {
		t.Fatalf("org-a first watcher = %d", got)
	}
	// Same live, other organization: its own watcher count starts at 1.
	if got := st.Attach("org-b", "user-b", "live1"); got != 1 {
		t.Fatalf("org-b must not share org-a's watchers, got %d", got)
	}
	// Same user id in another org is a different attachment.
	if got := st.Attach("org-b", "user-a", "live1"); got != 2 {
		t.Fatalf("org-b second watcher = %d", got)
	}
	if got := st.Detach("org-a", "user-a", "live1"); got != 0 {
		t.Fatalf("org-a last watcher left, remaining = %d", got)
	}
	if got := st.Watchers("org-b", "live1"); got != 2 {
		t.Fatalf("org-b watchers after org-a left = %d", got)
	}
	if st.OrgWatchers("org-a") != 0 || st.OrgWatchers("org-b") != 2 {
		t.Fatalf("org watchers a=%d b=%d", st.OrgWatchers("org-a"), st.OrgWatchers("org-b"))
	}
	st.DropOrg("org-b")
	if st.Watchers("org-b", "live1") != 0 || st.OrgWatchers("org-b") != 0 {
		t.Fatal("DropOrg must forget every watcher of the organization")
	}
}

// --- Fakes ---

type fakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	deleted []string
}

func newFakeStore() *fakeStore { return &fakeStore{objects: map[string][]byte{}} }

func (s *fakeStore) EnsureBucket(ctx context.Context) error { return nil }
func (s *fakeStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), data...)
	return nil
}
func (s *fakeStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, errFakeNotFound
	}
	return data, nil
}
func (s *fakeStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	s.deleted = append(s.deleted, key)
	return nil
}

var errFakeNotFound = errors.New("fake not found")

type fakeWaha struct {
	cfg        whatsapp.Config
	sent       []string
	mediaData  []byte
	mu         sync.Mutex
	states     []whatsapp.SessionStatus // successive Status() answers
	startCalls int
	created    int
	restarted  int
	contact    whatsapp.Contact // canned Contact() answer
	contactErr error
}

func (f *fakeWaha) StartSession(ctx context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	current := f.current()
	switch {
	case !current.Found:
		f.created++
	case current.NeedsRestart():
		f.restarted++
	}
	return nil
}
func (f *fakeWaha) QR(ctx context.Context, session string) ([]byte, error) { return []byte("png"), nil }

// Status hands back the queued states in order (the last one repeats), so a test
// can watch WAHA walk from STOPPED to SCAN_QR_CODE after a restart.
func (f *fakeWaha) Status(ctx context.Context, session string) (whatsapp.SessionStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.states) > 1 {
		current := f.states[0]
		f.states = f.states[1:]
		return current, nil
	}
	return f.current(), nil
}

// current is the state WAHA is in right now: the first queued one, or paired when
// the test has no opinions.
func (f *fakeWaha) current() whatsapp.SessionStatus {
	if len(f.states) > 0 {
		return f.states[0]
	}
	return whatsapp.SessionStatus{Found: true, Status: "WORKING", Connected: true, MePhone: "5511999990000"}
}
func (f *fakeWaha) Logout(ctx context.Context, session string) error { return nil }
func (f *fakeWaha) SendText(ctx context.Context, session, chatID, text string) (string, error) {
	f.sent = append(f.sent, chatID+"|"+text)
	return "wa-msg-1", nil
}
func (f *fakeWaha) DownloadMedia(ctx context.Context, rawURL string) ([]byte, error) {
	return f.mediaData, nil
}
func (f *fakeWaha) Contact(ctx context.Context, session, chatID string) (whatsapp.Contact, error) {
	if f.contactErr != nil {
		return whatsapp.Contact{}, f.contactErr
	}
	return f.contact, nil
}
func (f *fakeWaha) Config() whatsapp.Config { return f.cfg }

// fakeRepo implements only the Fila PIX methods the service touches; the
// embedded interface keeps the model.Repository contract satisfied.
type fakeRepo struct {
	model.Repository

	mu       sync.Mutex
	sessions map[string]model.PixWhatsAppSession
	contacts map[string]model.PixContact
	tickets  map[int64]model.PixTicket
	messages []model.PixMessage
	nextID   int64
	media    map[int64]model.PixMessage
	values   []int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		sessions: map[string]model.PixWhatsAppSession{},
		contacts: map[string]model.PixContact{},
		tickets:  map[int64]model.PixTicket{},
		media:    map[int64]model.PixMessage{},
	}
}

func (r *fakeRepo) UpsertPixSession(orgID, sessionName, status, mePhone, meJID string, connectedAt *time.Time) (model.PixWhatsAppSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := model.PixWhatsAppSession{
		OrgID:       orgID,
		SessionName: sessionName,
		Status:      status,
		MePhone:     mePhone,
		MeJID:       meJID,
	}
	if connectedAt != nil {
		session.ConnectedAt = connectedAt.UTC().Format(time.RFC3339Nano)
	}
	r.sessions[sessionName] = session
	return session, nil
}

func (r *fakeRepo) GetPixSessionByName(sessionName string) (model.PixWhatsAppSession, error) {
	s, ok := r.sessions[sessionName]
	if !ok {
		return model.PixWhatsAppSession{}, model.ErrPixNotFound
	}
	return s, nil
}

func (r *fakeRepo) GetPixSessionByOrg(orgID string) (model.PixWhatsAppSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		if s.OrgID == orgID {
			return s, nil
		}
	}
	return model.PixWhatsAppSession{}, model.ErrPixNotFound
}

func (r *fakeRepo) UpsertPixContact(owner, phone, jid, name string, at time.Time) (model.PixContact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := owner + "|" + jid
	c, ok := r.contacts[key]
	if !ok {
		r.nextID++
		c = model.PixContact{ID: r.nextID, OrgID: owner, PhoneE164: phone, WhatsAppJID: jid}
	}
	c.PushName = name
	r.contacts[key] = c
	return c, nil
}

func (r *fakeRepo) SetPixContactName(owner string, contactID int64, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, c := range r.contacts {
		if c.OrgID == owner && c.ID == contactID && c.PushName == "" {
			c.PushName = name
			r.contacts[key] = c
		}
	}
	return nil
}

func (r *fakeRepo) GetPixContactByID(owner string, id int64) (model.PixContact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.contacts {
		if c.OrgID == owner && c.ID == id {
			return c, nil
		}
	}
	return model.PixContact{}, model.ErrPixNotFound
}

func (r *fakeRepo) GetPendingPixTicket(owner string, contactID int64) (model.PixTicket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tickets {
		if t.OrgID == owner && t.ContactID == contactID && t.Status == model.PixTicketPending {
			return t, nil
		}
	}
	return model.PixTicket{}, model.ErrPixNotFound
}

func (r *fakeRepo) CreatePixTicket(owner string, contactID int64, at time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tickets {
		if t.OrgID == owner && t.ContactID == contactID && t.Status == model.PixTicketPending {
			return t.ID, nil
		}
	}
	r.nextID++
	id := r.nextID
	r.tickets[id] = model.PixTicket{ID: id, OrgID: owner, ContactID: contactID, Status: model.PixTicketPending, ReceivedAt: at.UTC().Format(time.RFC3339Nano)}
	return id, nil
}

func (r *fakeRepo) GetPixTicket(owner string, id int64) (model.PixTicket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickets[id]
	if !ok || t.OrgID != owner {
		return model.PixTicket{}, model.ErrPixNotFound
	}
	for _, c := range r.contacts {
		if c.ID == t.ContactID {
			t.Contact = c
			break
		}
	}
	return t, nil
}

func (r *fakeRepo) MarkPixTicketReceipt(owner string, id int64, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.tickets[id]
	t.HasReceipt = true
	r.tickets[id] = t
	return nil
}

func (r *fakeRepo) MarkPixAutoReplySent(owner string, ticketID int64, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickets[ticketID]
	if !ok || t.OrgID != owner {
		return model.ErrPixNotFound
	}
	t.AutoReplySent = true
	r.tickets[ticketID] = t
	return nil
}

func (r *fakeRepo) AddPixMessage(m model.PixMessage) (int64, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.messages {
		if existing.OrgID == m.OrgID && existing.WhatsAppMessageID == m.WhatsAppMessageID {
			return 0, false, nil
		}
	}
	r.nextID++
	m.ID = r.nextID
	r.messages = append(r.messages, m)
	if m.MediaPath != "" {
		r.media[m.ID] = m
	}
	return m.ID, true, nil
}

func (r *fakeRepo) ListActivePixMedia(owner string, limit int) ([]model.PixMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []model.PixMessage{}
	for _, m := range r.media {
		if m.OrgID == owner && m.MediaDeletedAt == "" && m.MediaPath != "" {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeRepo) MarkPixMediaDeleted(owner string, id int64, at time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.media[id]
	m.MediaDeletedAt = at.UTC().Format(time.RFC3339Nano)
	r.media[id] = m
	return nil
}

func (r *fakeRepo) MarkPixTicketAnswered(owner string, id int64, answeredBy string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickets[id]
	if !ok || t.OrgID != owner {
		return model.ErrPixNotFound
	}
	if t.Status == model.PixTicketAnswered {
		return nil
	}
	t.Status = model.PixTicketAnswered
	r.tickets[id] = t
	return nil
}

func (r *fakeRepo) countMessages() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

// --- value gate fakes ---

func (r *fakeRepo) ListPixValueRules(owner string) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.values...), nil
}

func (r *fakeRepo) ReplacePixValueRules(owner string, cents []int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append([]int64(nil), cents...)
	return nil
}

func (r *fakeRepo) GetPixTicketPaidTotal(owner string, ticketID int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var total int64
	for _, m := range r.messages {
		if m.OrgID == owner && m.TicketID == ticketID && m.Direction == model.PixDirectionInbound &&
			(m.Type == model.PixTypeImage || m.Type == model.PixTypeDocument) && m.MediaValueCents > 0 {
			total += m.MediaValueCents
		}
	}
	return total, nil
}

func (r *fakeRepo) DeleteEmptyPixTicket(owner string, ticketID int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickets[ticketID]
	if !ok || t.OrgID != owner || t.Status != model.PixTicketPending {
		return false, nil
	}
	for _, m := range r.messages {
		if m.TicketID == ticketID {
			return false, nil
		}
	}
	delete(r.tickets, ticketID)
	return true, nil
}

func (r *fakeRepo) ListPixTickets(owner, status string, limit int) ([]model.PixTicket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if status == "" {
		status = model.PixTicketPending
	}
	out := []model.PixTicket{}
	for _, t := range r.tickets {
		if t.OrgID != owner || t.Status != status {
			continue
		}
		for _, c := range r.contacts {
			if c.ID == t.ContactID {
				t.Contact = c
				break
			}
		}
		for _, m := range r.messages {
			if m.TicketID != t.ID {
				continue
			}
			t.MessageCount++
			if m.Direction != model.PixDirectionInbound {
				continue
			}
			switch m.Type {
			case model.PixTypeText:
				t.HasInboundText = true
			case model.PixTypeImage, model.PixTypeDocument:
				if m.MediaValueCents > 0 {
					t.PaidTotalCents += m.MediaValueCents
				} else {
					t.HasUnextractedReceipt = true
				}
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// --- value gate tests ---

func jpegReceipt() []byte {
	return append([]byte{0xFF, 0xD8, 0xFF}, []byte("jpeg-bytes")...)
}

func gateService(t *testing.T, text string, extractErr error) (*PixQueueService, *fakeRepo, *fakeWaha) {
	t.Helper()
	repo := newFakeRepo()
	waha := &fakeWaha{cfg: enabledWahaConfig(), mediaData: jpegReceipt()}
	svc := newTestService(repo, waha, newFakeStore())
	svc.extractText = func(context.Context, media.Receipt, []byte) (string, error) {
		return text, extractErr
	}
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}
	return svc, repo, waha
}

func receiptMsg(id string) whatsapp.InboundMessage {
	return whatsapp.InboundMessage{
		MessageID: id, Session: "pix_owner", JID: "5511@c.us", PhoneE164: "5511",
		Type: model.PixTypeImage, MediaURL: "http://waha/media.jpg", MediaMime: "image/jpeg",
		Timestamp: time.Now(),
	}
}

func TestValueGatePartialKeepsTicketHiddenAndReplies(t *testing.T) {
	svc, repo, waha := gateService(t, "Valor da transferência R$ 8,00", nil)
	repo.values = []int64{1000, 1500}
	events := []string{}
	svc.SetBroadcaster(func(_, eventType string, data interface{}) { events = append(events, eventType) })

	if err := svc.HandleInbound(context.Background(), receiptMsg("g1")); err != nil {
		t.Fatalf("partial receipt: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("partial payment must not emit SSE, got %v", events)
	}
	if len(waha.sent) != 1 || !strings.Contains(waha.sent[0], "Falta R$ 2,00") {
		t.Fatalf("expected gap reply with missing amount, got %v", waha.sent)
	}
	if !strings.Contains(waha.sent[0], "R$ 8,00") || !strings.Contains(waha.sent[0], "R$ 10,00") {
		t.Fatalf("gap reply must cite received and target values: %v", waha.sent)
	}
	// The partial receipt is stored (it accumulates) but the queue hides it.
	if repo.countMessages() == 0 {
		t.Fatal("partial receipt must be stored")
	}
	tickets, err := svc.ListTickets("owner-1", model.PixTicketPending, 10)
	if err != nil || len(tickets) != 0 {
		t.Fatalf("partial-only ticket must stay out of the queue: %d %v", len(tickets), err)
	}
}

func TestValueGatePartialThenCompleteEntersQueue(t *testing.T) {
	svc, repo, waha := gateService(t, "R$ 8,00", nil)
	repo.values = []int64{1000, 1500}
	events := []string{}
	svc.SetBroadcaster(func(_, eventType string, data interface{}) { events = append(events, eventType) })

	if err := svc.HandleInbound(context.Background(), receiptMsg("c1")); err != nil {
		t.Fatalf("first receipt: %v", err)
	}
	// Second receipt closes the 10,00 target exactly.
	svc.extractText = func(context.Context, media.Receipt, []byte) (string, error) {
		return "comprovante de 2,00", nil
	}
	if err := svc.HandleInbound(context.Background(), receiptMsg("c2")); err != nil {
		t.Fatalf("second receipt: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("completing the total must emit the ticket update")
	}
	tickets, err := svc.ListTickets("owner-1", model.PixTicketPending, 10)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("completed ticket must be visible: %d %v", len(tickets), err)
	}
	if tickets[0].PaidTotalCents != 1000 {
		t.Fatalf("paid total = %d, want 1000", tickets[0].PaidTotalCents)
	}
	if len(waha.sent) != 1 {
		t.Fatalf("only the partial should have been answered, got %v", waha.sent)
	}
}

func TestValueGateUnreadableReceiptIsDiscarded(t *testing.T) {
	svc, repo, waha := gateService(t, "comprovante sem valor nenhum legivel", nil)
	repo.values = []int64{1000}
	events := []string{}
	svc.SetBroadcaster(func(_, eventType string, data interface{}) { events = append(events, eventType) })

	if err := svc.HandleInbound(context.Background(), receiptMsg("u1")); err != nil {
		t.Fatalf("unreadable receipt: %v", err)
	}
	if repo.countMessages() != 0 {
		t.Fatalf("unreadable receipt must not be stored, got %d messages", repo.countMessages())
	}
	if len(repo.tickets) != 0 {
		t.Fatalf("empty ticket must be deleted, got %d", len(repo.tickets))
	}
	if len(waha.sent) != 1 || !strings.Contains(waha.sent[0], "Não foi possível identificar o valor") {
		t.Fatalf("expected unreadable-value reply, got %v", waha.sent)
	}
	if len(events) != 0 {
		t.Fatalf("nothing must be emitted, got %v", events)
	}
}

func TestValueGateOverPaymentEntersQueue(t *testing.T) {
	svc, repo, _ := gateService(t, "R$ 16,00", nil)
	repo.values = []int64{1000, 1500}
	events := []string{}
	svc.SetBroadcaster(func(_, eventType string, data interface{}) { events = append(events, eventType) })

	if err := svc.HandleInbound(context.Background(), receiptMsg("o1")); err != nil {
		t.Fatalf("over receipt: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("overpayment must surface for the operator")
	}
	tickets, err := svc.ListTickets("owner-1", model.PixTicketPending, 10)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("overpaid ticket must be visible: %d %v", len(tickets), err)
	}
}

func TestValueGateEngineDownFailsOpen(t *testing.T) {
	svc, repo, _ := gateService(t, "", receipt.ErrEngineUnavailable)
	repo.values = []int64{1000}
	events := []string{}
	svc.SetBroadcaster(func(_, eventType string, data interface{}) { events = append(events, eventType) })

	if err := svc.HandleInbound(context.Background(), receiptMsg("e1")); err != nil {
		t.Fatalf("receipt with engine down: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("OCR engine failure must not block the queue (fail-open)")
	}
	tickets, err := svc.ListTickets("owner-1", model.PixTicketPending, 10)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("fail-open ticket must stay visible: %d %v", len(tickets), err)
	}
	if !tickets[0].HasUnextractedReceipt {
		t.Fatal("fail-open receipt must be flagged as unextracted")
	}
}

func TestValueGateTextTicketStaysVisible(t *testing.T) {
	svc, repo, _ := gateService(t, "R$ 8,00", nil)
	repo.values = []int64{1000}

	text := whatsapp.InboundMessage{
		MessageID: "t1", Session: "pix_owner", JID: "5511@c.us", PhoneE164: "5511",
		Type: model.PixTypeText, Body: "paguei parcial", Timestamp: time.Now(),
	}
	if err := svc.HandleInbound(context.Background(), text); err != nil {
		t.Fatalf("text: %v", err)
	}
	if err := svc.HandleInbound(context.Background(), receiptMsg("t2")); err != nil {
		t.Fatalf("partial receipt: %v", err)
	}
	tickets, err := svc.ListTickets("owner-1", model.PixTicketPending, 10)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("a ticket with client text must stay visible: %d %v", len(tickets), err)
	}
}

func TestValueGateOffStoresEverything(t *testing.T) {
	svc, _, _ := gateService(t, "", errors.New("must not be called"))
	events := []string{}
	svc.SetBroadcaster(func(_, eventType string, data interface{}) { events = append(events, eventType) })

	if err := svc.HandleInbound(context.Background(), receiptMsg("off1")); err != nil {
		t.Fatalf("receipt with gate off: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("gate off must keep the current flow (emit)")
	}
	tickets, err := svc.ListTickets("owner-1", model.PixTicketPending, 10)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("gate off must list everything: %d %v", len(tickets), err)
	}
}

func TestReplacePixValuesValidation(t *testing.T) {
	svc, repo, _ := gateService(t, "", nil)
	if err := svc.ReplacePixValues("owner-1", []int64{1500, 1000, 1500}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, _ := repo.ListPixValueRules("owner-1")
	if len(got) != 2 || got[0] != 1000 || got[1] != 1500 {
		t.Fatalf("values = %v, want sorted unique [1000 1500]", got)
	}
	if err := svc.ReplacePixValues("owner-1", []int64{0}); err == nil {
		t.Fatal("zero must be rejected")
	}
	if err := svc.ReplacePixValues("owner-1", []int64{1_000_000_000}); err == nil {
		t.Fatal("amount above the limit must be rejected")
	}
	many := make([]int64, maxPixValuesPerOrg+1)
	for i := range many {
		many[i] = int64(i + 1)
	}
	if err := svc.ReplacePixValues("owner-1", many); err == nil {
		t.Fatal("more than the cap must be rejected")
	}
}

// A privacy-restricted sender arrives as an @lid with no phone and no pushName;
// the service must resolve the profile name through WAHA so the queue shows a
// person instead of an opaque id.
func TestHandleInboundEnrichesLIDContactName(t *testing.T) {
	repo := newFakeRepo()
	waha := &fakeWaha{
		cfg:     enabledWahaConfig(),
		contact: whatsapp.Contact{ID: "37048606048491@lid", Pushname: "Thiago Henrique"},
	}
	svc := newTestService(repo, waha, newFakeStore())
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}

	msg := whatsapp.InboundMessage{
		MessageID: "lid-1", Session: "pix_owner", JID: "37048606048491@lid",
		Type: model.PixTypeText, Body: "oi", Timestamp: time.Now(),
	}
	if err := svc.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if c := repo.contacts["owner-1|37048606048491@lid"]; c.PushName != "Thiago Henrique" {
		t.Fatalf("contact not enriched from WAHA: %+v", c)
	}
}

// A webhook that already carries a pushName must not trigger a contact lookup.
func TestHandleInboundSkipsContactLookupWhenNamePresent(t *testing.T) {
	repo := newFakeRepo()
	waha := &fakeWaha{cfg: enabledWahaConfig(), contactErr: errors.New("must not be called")}
	svc := newTestService(repo, waha, newFakeStore())
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}

	msg := whatsapp.InboundMessage{
		MessageID: "named-1", Session: "pix_owner", JID: "5511999998888@c.us",
		PhoneE164: "5511999998888", PushName: "Fulano",
		Type: model.PixTypeText, Body: "oi", Timestamp: time.Now(),
	}
	if err := svc.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if c := repo.contacts["owner-1|5511999998888@c.us"]; c.PushName != "Fulano" {
		t.Fatalf("webhook name should win: %+v", c)
	}
}

func newTestService(repo *fakeRepo, waha *fakeWaha, store *fakeStore) *PixQueueService {
	svc := NewPixQueueService(repo, nil, store)
	svc.waha = waha
	svc.pairingWait = 60 * time.Millisecond
	svc.pairingInterval = 10 * time.Millisecond
	return svc
}

func enabledWahaConfig() whatsapp.Config {
	return whatsapp.Config{Enabled: true, BaseURL: "http://waha:3000", APIKey: "k", WebhookSecret: "s", MaxMediaBytes: 1 << 20}
}

// --- Service tests ---

func TestConnectRestoresParkedSession(t *testing.T) {
	waha := &fakeWaha{cfg: enabledWahaConfig(), states: []whatsapp.SessionStatus{
		{Found: true, Status: whatsapp.StatusStopped}, // WAHA parked it after a logout
		{Found: true, Status: whatsapp.StatusScanQRCode},
	}}
	svc := newTestService(newFakeRepo(), waha, newFakeStore())

	session, err := svc.Connect(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if waha.restarted != 1 {
		t.Fatalf("a parked session must be restarted to get a QR, restarts = %d", waha.restarted)
	}
	if session.Status != model.PixSessionScanQR {
		t.Fatalf("status = %q, want %q", session.Status, model.PixSessionScanQR)
	}
}

func TestConnectLeavesPairedSessionAlone(t *testing.T) {
	waha := &fakeWaha{cfg: enabledWahaConfig(), states: []whatsapp.SessionStatus{
		{Found: true, Status: "WORKING", Connected: true, MePhone: "5511999990000"},
	}}
	svc := newTestService(newFakeRepo(), waha, newFakeStore())

	session, err := svc.Connect(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if waha.restarted != 0 || waha.created != 0 {
		t.Fatalf("restarting a paired number would log it out: creates=%d restarts=%d", waha.created, waha.restarted)
	}
	if session.Status != model.PixSessionConnected {
		t.Fatalf("status = %q, want %q", session.Status, model.PixSessionConnected)
	}
}

func TestQRStartsParkedSessionBeforeAsking(t *testing.T) {
	waha := &fakeWaha{cfg: enabledWahaConfig(), states: []whatsapp.SessionStatus{
		{Found: true, Status: whatsapp.StatusStopped},
		{Found: true, Status: whatsapp.StatusStarting},
		{Found: true, Status: whatsapp.StatusScanQRCode},
	}}
	svc := newTestService(newFakeRepo(), waha, newFakeStore())

	png, err := svc.QR(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("qr: %v", err)
	}
	if string(png) != "png" {
		t.Fatalf("png = %q", string(png))
	}
	if waha.restarted != 1 {
		t.Fatalf("restarts = %d, want 1", waha.restarted)
	}
}

func TestQRRejectsAlreadyPairedNumber(t *testing.T) {
	waha := &fakeWaha{cfg: enabledWahaConfig(), states: []whatsapp.SessionStatus{
		{Found: true, Status: "WORKING", Connected: true, MePhone: "5511999990000"},
	}}
	svc := newTestService(newFakeRepo(), waha, newFakeStore())

	if _, err := svc.QR(context.Background(), "owner-1"); !errors.Is(err, ErrPixAlreadyPaired) {
		t.Fatalf("err = %v, want ErrPixAlreadyPaired", err)
	}
}

func TestQRReportsPendingWhenWAHASpendsTooLong(t *testing.T) {
	waha := &fakeWaha{cfg: enabledWahaConfig(), states: []whatsapp.SessionStatus{
		{Found: true, Status: whatsapp.StatusStopped},
		{Found: true, Status: whatsapp.StatusStarting}, // WAHA never reaches SCAN_QR_CODE
	}}
	svc := newTestService(newFakeRepo(), waha, newFakeStore())

	if _, err := svc.QR(context.Background(), "owner-1"); !errors.Is(err, ErrPixQRPending) {
		t.Fatalf("err = %v, want ErrPixQRPending", err)
	}
}

func TestHandleInboundTextCreatesTicket(t *testing.T) {
	repo := newFakeRepo()
	waha := &fakeWaha{cfg: enabledWahaConfig()}
	store := newFakeStore()
	svc := newTestService(repo, waha, store)
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}

	events := make([]string, 0)
	svc.SetBroadcaster(func(_, eventType string, data interface{}) { events = append(events, eventType) })

	msg := whatsapp.InboundMessage{
		MessageID: "m1", Session: "pix_owner", JID: "5511@c.us", PhoneE164: "5511",
		PushName: "Fulano", Type: model.PixTypeText, Body: "oi", Timestamp: time.Now(),
	}
	if err := svc.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("handle text: %v", err)
	}
	if repo.countMessages() != 1 {
		t.Fatalf("expected 1 message, got %d", repo.countMessages())
	}
	if len(events) == 0 || events[0] != "pix-ticket-update" {
		t.Fatalf("expected SSE event, got %v", events)
	}
}

func TestPixBroadcasterReceivesOrgID(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeWaha{cfg: enabledWahaConfig()}, newFakeStore())
	repo.sessions["pix_a"] = model.PixWhatsAppSession{OrgID: "org-a", SessionName: "pix_a"}
	repo.sessions["pix_b"] = model.PixWhatsAppSession{OrgID: "org-b", SessionName: "pix_b"}

	type event struct{ org, kind string }
	var mu sync.Mutex
	var events []event
	svc.SetBroadcaster(func(orgID, eventType string, data interface{}) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event{orgID, eventType})
	})
	orgsSeen := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for _, e := range events {
			out[e.org]++
		}
		return out
	}

	for _, session := range []string{"pix_a", "pix_b"} {
		msg := whatsapp.InboundMessage{
			MessageID: "m-" + session, Session: session, JID: "5511@c.us", PhoneE164: "5511",
			PushName: "Fulano", Type: model.PixTypeText, Body: "oi", Timestamp: time.Now(),
		}
		if err := svc.HandleInbound(context.Background(), msg); err != nil {
			t.Fatalf("handle %s: %v", session, err)
		}
	}
	seen := orgsSeen()
	if seen["org-a"] == 0 || seen["org-b"] == 0 || len(seen) != 2 {
		t.Fatalf("each inbound must broadcast to its own organization only, got %v", events)
	}

	ticketsA, err := svc.ListTickets("org-a", model.PixTicketPending, 10)
	if err != nil || len(ticketsA) != 1 {
		t.Fatalf("org-a tickets = %d, %v", len(ticketsA), err)
	}
	ticketsB, err := svc.ListTickets("org-b", model.PixTicketPending, 10)
	if err != nil || len(ticketsB) != 1 {
		t.Fatalf("org-b tickets = %d, %v", len(ticketsB), err)
	}

	mu.Lock()
	events = nil
	mu.Unlock()
	if err := svc.Answer("org-b", "user-b", ticketsA[0].ID); err == nil {
		t.Fatal("org-b must not answer org-a's ticket")
	}
	if got := orgsSeen(); len(got) != 0 {
		t.Fatalf("a rejected answer must not broadcast, got %v", got)
	}
	if err := svc.Answer("org-a", "user-a", ticketsA[0].ID); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if got := orgsSeen(); got["org-a"] == 0 || len(got) != 1 {
		t.Fatalf("answer must broadcast only to org-a, got %v", got)
	}
}

func TestHandleInboundReceiptStoresMedia(t *testing.T) {
	repo := newFakeRepo()
	jpeg := append([]byte{0xFF, 0xD8, 0xFF}, []byte("data")...)
	waha := &fakeWaha{cfg: enabledWahaConfig(), mediaData: jpeg}
	store := newFakeStore()
	svc := newTestService(repo, waha, store)
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}

	msg := whatsapp.InboundMessage{
		MessageID: "m2", Session: "pix_owner", JID: "5511@c.us", PhoneE164: "5511",
		Type: model.PixTypeImage, MediaURL: "http://waha/media.jpg", MediaMime: "image/jpeg",
		MediaFilename: "../evil.jpg", Timestamp: time.Now(),
	}
	if err := svc.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("handle receipt: %v", err)
	}
	if repo.countMessages() != 1 {
		t.Fatalf("expected 1 message, got %d", repo.countMessages())
	}
	var stored model.PixMessage
	for _, m := range repo.messages {
		stored = m
	}
	if stored.MediaPath == "" || stored.MediaFilename != "evil.jpg" {
		t.Fatalf("media not stored safely: %+v", stored)
	}
	if len(store.objects) != 1 {
		t.Fatalf("expected object in store, got %d", len(store.objects))
	}
}

func TestHandleInboundUnsupportedSendsAutoReply(t *testing.T) {
	repo := newFakeRepo()
	waha := &fakeWaha{cfg: enabledWahaConfig()}
	svc := newTestService(repo, waha, newFakeStore())
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}

	msg := whatsapp.InboundMessage{
		MessageID: "m3", Session: "pix_owner", JID: "5511@c.us", PhoneE164: "5511",
		Type: model.PixTypeUnsupported, Timestamp: time.Now(),
	}
	if err := svc.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("handle unsupported: %v", err)
	}
	if len(waha.sent) != 1 {
		t.Fatalf("expected auto reply, got %d", len(waha.sent))
	}
	// A second unsupported message must not auto reply again (once per ticket).
	if err := svc.HandleInbound(context.Background(), whatsapp.InboundMessage{
		MessageID: "m4", Session: "pix_owner", JID: "5511@c.us", PhoneE164: "5511",
		Type: model.PixTypeUnsupported, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("handle unsupported 2: %v", err)
	}
	if len(waha.sent) != 1 {
		t.Fatalf("auto reply should be sent once per ticket, got %d", len(waha.sent))
	}
	if repo.countMessages() != 3 {
		t.Fatalf("expected 3 messages (2 inbound + auto reply), got %d", repo.countMessages())
	}
}

func TestSendTextRejectsAnsweredTicket(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeWaha{cfg: enabledWahaConfig()}, newFakeStore())
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}
	contact, _ := repo.UpsertPixContact("owner-1", "5511", "5511@c.us", "F", time.Now())
	id, _ := repo.CreatePixTicket("owner-1", contact.ID, time.Now())
	if err := repo.MarkPixTicketAnswered("owner-1", id, "owner-1", time.Now()); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if _, err := svc.SendText(context.Background(), "owner-1", id, "olá"); !errors.Is(err, model.ErrPixInvalidTransition) {
		t.Fatalf("expected invalid transition, got %v", err)
	}
}

func TestPurgeOrgMediaDeletesAndFlags(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	svc := newTestService(repo, &fakeWaha{cfg: enabledWahaConfig()}, store)
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}
	contact, _ := repo.UpsertPixContact("owner-1", "5511", "5511@c.us", "F", time.Now())
	id, _ := repo.CreatePixTicket("owner-1", contact.ID, time.Now())
	key := "pix-media/owner-1/x.jpg"
	_, _, _ = repo.AddPixMessage(model.PixMessage{
		OrgID: "owner-1", TicketID: id, ContactID: contact.ID, WhatsAppMessageID: "m1",
		Direction: model.PixDirectionInbound, Type: model.PixTypeImage, MediaPath: key,
	})
	_ = store.Put(context.Background(), key, []byte("x"), "image/jpeg")

	removed, err := svc.PurgeOrgMedia(context.Background(), "owner-1", "live disconnected")
	if err != nil || removed != 1 {
		t.Fatalf("purge: removed=%d err=%v", removed, err)
	}
	if len(store.objects) != 0 {
		t.Fatalf("object not deleted: %v", store.objects)
	}
	active, _ := repo.ListActivePixMedia("owner-1", 10)
	if len(active) != 0 {
		t.Fatalf("expected no active media, got %d", len(active))
	}
}

// Listing the queue resolves the display name of a contact stored without one
// (@lid senders arrive nameless) through WAHA, once per JID.
func TestListTicketsBackfillsContactName(t *testing.T) {
	repo := newFakeRepo()
	waha := &fakeWaha{
		cfg:     enabledWahaConfig(),
		contact: whatsapp.Contact{ID: "37048606048491@lid", Pushname: "Thiago Henrique"},
	}
	svc := newTestService(repo, waha, newFakeStore())
	repo.sessions["pix_owner"] = model.PixWhatsAppSession{OrgID: "owner-1", SessionName: "pix_owner"}

	// Seed a contact + pending ticket with an empty push name.
	contact, err := repo.UpsertPixContact("owner-1", "", "37048606048491@lid", "", time.Now())
	if err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	if _, err := repo.CreatePixTicket("owner-1", contact.ID, time.Now()); err != nil {
		t.Fatalf("seed ticket: %v", err)
	}

	tickets, err := svc.ListTickets("owner-1", model.PixTicketPending, 10)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("list: %d %v", len(tickets), err)
	}
	if tickets[0].Contact.PushName != "Thiago Henrique" {
		t.Fatalf("contact name not backfilled: %+v", tickets[0].Contact)
	}
	// The name is persisted so the next list does not re-query WAHA.
	stored, _ := repo.GetPixContactByID("owner-1", contact.ID)
	if stored.PushName != "Thiago Henrique" {
		t.Fatalf("backfilled name not persisted: %+v", stored)
	}
}
