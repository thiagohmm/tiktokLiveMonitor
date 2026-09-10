package monitor

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// testRef builds a live reference for tests that do not need a real session row.
func testRef(name string) model.LiveRef {
	return model.LiveRef{ID: name + "-session", Name: name}
}

func TestNormalizeID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  User1  ", "user1"},
		{"UPPER", "upper"},
		{"MiXeD", "mixed"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeID(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFoldText(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  Hello World  ", "hello world"},
		{"Voce gosta?", "voce gosta?"},
		{"COMO ASSIM", "como assim"},
		{"", ""},
	}
	for _, tt := range tests {
		got := foldText(tt.input)
		if got != tt.expected {
			t.Errorf("foldText(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestLooksLikeQuestion(t *testing.T) {
	questions := []string{
		"Qual sua religiao?",
		"Como vai?",
		"Pq isso?",
		"PK isso?",
		"Por que voce fez isso",
		"Tem como me ajudar?",
		"Da pra explicar?",
		"Alguem sabe a resposta",
		"Me tira uma duvida",
		"¿Que hora es?",
	}
	for _, q := range questions {
		if !looksLikeQuestion(q) {
			t.Errorf("expected %q to be a question", q)
		}
	}

	notQuestions := []string{
		"Boa noite",
		"Oi pessoal",
		"Show a live",
		"Valeu streamer",
		"",
	}
	for _, q := range notQuestions {
		if looksLikeQuestion(q) {
			t.Errorf("expected %q NOT to be a question", q)
		}
	}
}

func TestDetectKeyword(t *testing.T) {
	m := &Monitor{
		settings: Settings{
			TargetGifts: []string{"dino", "perfume"},
		},
	}
	tests := []struct {
		input    string
		expected string
	}{
		{"O dino apareceu", "dino"},
		{"Envia perfume", "perfume"},
		{"DINO gigante", "dino"},
		{"Boa live", ""},
		{"Oi pessoal", ""},
	}
	for _, tt := range tests {
		got := m.detectKeyword(tt.input)
		if got != tt.expected {
			t.Errorf("detectKeyword(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsTargetGift(t *testing.T) {
	m := &Monitor{
		settings: Settings{
			TargetGifts: []string{"perfume", "coração", "dino"},
		},
	}
	targets := []string{
		"Perfume",
		"Dino",
		"coração",
		"tiny dino",
	}
	for _, g := range targets {
		if !m.isTargetGift(g) {
			t.Errorf("expected %q to be a target gift", g)
		}
	}

	notTargets := []string{
		"Rose",
		"Tiger",
		"Coffee",
		"Galaxy",
	}
	for _, g := range notTargets {
		if m.isTargetGift(g) {
			t.Errorf("expected %q NOT to be a target gift", g)
		}
	}
}

func TestCoalesce(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{[]string{"", "b", "c"}, "b"},
		{[]string{"", "", ""}, ""},
		{[]string{"first"}, "first"},
		{[]string{}, ""},
	}
	for _, tt := range tests {
		got := coalesce(tt.input...)
		if got != tt.expected {
			t.Errorf("coalesce(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func newMonitorWithDB(t *testing.T) (*Monitor, *database.DB) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL não configurado: testes exigem PostgreSQL descartável")
	}
	dsn, cleanup, err := database.CreateTestDatabase(baseDSN)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(cleanup)
	db, err := database.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	m, err := New()
	if err != nil {
		t.Fatalf("new monitor: %v", err)
	}
	m.SetRepo(db)
	m.SetCurrentLive("live1")
	return m, db
}

func TestBeginOrResumeSessionRestoresBuffer(t *testing.T) {
	m, db := newMonitorWithDB(t)

	session, err := db.BeginLiveSession("live1", time.Now())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	ref := model.LiveRef{ID: session.ID, Name: "live1"}
	if err := db.AddUserMessageDedup(ref, "user1", "User One", "hello today"); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if err := db.LogAnomaly(ref, "spam", true, "SPAM", "user1"); err != nil {
		t.Fatalf("log anomaly: %v", err)
	}
	if _, err := db.AddGift(ref, "user1", "User One", "Rose", 1, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}

	m.beginOrResumeSession()

	if got := m.CurrentLiveID(); got != session.ID {
		t.Fatalf("expected to resume session %s, got %q", session.ID, got)
	}
	if len(m.GetChatBuffer()) == 0 {
		t.Fatal("expected chat buffer to be restored")
	}
	if !m.IsPinnedUser("user1") {
		t.Fatal("expected pinned user to be restored")
	}
	gifts, err := db.GetRecentGifts("live1", 10)
	if err != nil {
		t.Fatalf("gifts: %v", err)
	}
	if len(gifts) != 1 {
		t.Fatalf("expected gift to remain, got %d", len(gifts))
	}
}

// A new day must start a new session WITHOUT deleting the previous live's rows:
// the old data stays in the database, addressed by its own id.
func TestBeginOrResumeSessionKeepsPreviousHistory(t *testing.T) {
	m, db := newMonitorWithDB(t)

	oldAt := time.Now().UTC().Add(-25 * time.Hour)
	oldSession, err := db.BeginLiveSession("live1", oldAt)
	if err != nil {
		t.Fatalf("begin old session: %v", err)
	}
	oldRef := model.LiveRef{ID: oldSession.ID, Name: "live1"}
	if _, err := db.AddGift(oldRef, "user1", "User One", "Rose", 1, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}
	if err := db.AddUserMessageDedup(oldRef, "user1", "User One", "old hello"); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if err := db.LogAnomaly(oldRef, "old", true, "SPAM", "user1"); err != nil {
		t.Fatalf("log anomaly: %v", err)
	}

	m.beginOrResumeSession()

	newID := m.CurrentLiveID()
	if newID == "" {
		t.Fatal("expected a session id")
	}
	if newID == oldSession.ID {
		t.Fatal("expected a NEW session on a different day")
	}

	// Nothing was deleted.
	gifts, err := db.GetRecentGifts("live1", 10)
	if err != nil {
		t.Fatalf("gifts: %v", err)
	}
	if len(gifts) != 1 {
		t.Fatalf("expected the previous live's gift to survive, got %d", len(gifts))
	}
	if _, err := db.GetLiveSession(oldSession.ID); err != nil {
		t.Fatalf("expected the previous session row to survive: %v", err)
	}

	// The previous live's data never leaks into the new session's buffers.
	if len(m.GetChatBuffer()) != 0 {
		t.Fatal("expected empty chat buffer: old session data must not be loaded")
	}
	if m.IsPinnedUser("user1") {
		t.Fatal("expected no pinned users from the previous session")
	}
}

// An admin can delete the live that is still streaming. The session must then be
// reopened, so later events do not become invisible orphan rows.
func TestTouchSessionReopensDeletedSession(t *testing.T) {
	m, db := newMonitorWithDB(t)
	m.beginOrResumeSession()

	first := m.CurrentLiveID()
	if first == "" {
		t.Fatal("expected a session id")
	}
	if _, err := db.DeleteLiveSession(first); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	// Clear the throttle window so the next touch hits the database.
	m.mu.Lock()
	m.liveTouchAt = time.Time{}
	m.mu.Unlock()
	m.touchSession()

	got := m.CurrentLiveID()
	if got == "" || got == first {
		t.Fatalf("expected a new session after the deletion, got %q (first %q)", got, first)
	}
	if _, err := db.GetLiveSession(got); err != nil {
		t.Fatalf("the reopened session must exist: %v", err)
	}
}

// Every emitted event must carry the session id, otherwise the controller would
// fall back to a per-event database lookup to resolve the session.
func TestEmitInjectsLiveID(t *testing.T) {
	m, _ := newMonitorWithDB(t)
	m.beginOrResumeSession()
	want := m.CurrentLiveID()
	if want == "" {
		t.Fatal("expected a session id")
	}

	var got EventData
	m.OnEvent(func(eventType string, data EventData) {
		got = data
	})

	src := EventData{"comment": "oi"}
	m.emit("test-event", src)

	if got["liveId"] != want {
		t.Fatalf("expected liveId %q in %#v", want, got)
	}
	if _, exists := src["liveId"]; exists {
		t.Fatal("emit must not mutate the caller's payload")
	}
}

func TestCoalesceStr(t *testing.T) {
	tests := []struct {
		val      string
		fallback string
		expected string
	}{
		{"hello", "world", "hello"},
		{"", "world", "world"},
		{"", "", ""},
	}
	for _, tt := range tests {
		got := coalesceStr(tt.val, tt.fallback)
		if got != tt.expected {
			t.Errorf("coalesceStr(%q, %q) = %q, want %q", tt.val, tt.fallback, got, tt.expected)
		}
	}
}

func TestParseGiftNames(t *testing.T) {
	tests := []struct {
		name string
		data EventData
		want []string
	}{
		{
			name: "interface slice",
			data: EventData{"gifts": []interface{}{"Rose", "", "Dino"}},
			want: []string{"Rose", "Dino"},
		},
		{
			name: "string slice",
			data: EventData{"gifts": []string{"Rose"}},
			want: []string{"Rose"},
		},
		{
			name: "missing",
			data: EventData{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGiftNames(tt.data)
			if len(got) != len(tt.want) {
				t.Fatalf("parseGiftNames() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("parseGiftNames()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractFromDataFollower(t *testing.T) {
	trueVal := true
	falseVal := false
	tests := []struct {
		name string
		data EventData
		want *bool
	}{
		{name: "bool true", data: EventData{"isFollower": true}, want: &trueVal},
		{name: "bool false", data: EventData{"isFollower": false}, want: &falseVal},
		{name: "float 1", data: EventData{"isFollower": float64(1)}, want: &trueVal},
		{name: "float 2 friends", data: EventData{"isFollower": float64(2)}, want: &trueVal},
		{name: "float 0", data: EventData{"isFollower": float64(0)}, want: &falseVal},
		{name: "string 1", data: EventData{"isFollower": "1"}, want: &trueVal},
		{name: "missing", data: EventData{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFromData(tt.data)
			if tt.want == nil {
				if got.IsFollower != nil {
					t.Fatalf("expected nil follower, got %v", *got.IsFollower)
				}
				return
			}
			if got.IsFollower == nil || *got.IsFollower != *tt.want {
				t.Fatalf("IsFollower = %v, want %v", got.IsFollower, *tt.want)
			}
		})
	}
}

func TestGiftsListCachesAndIgnoresEmptyOverwrite(t *testing.T) {
	m, _ := New()
	emitted := make(chan []string, 2)
	m.OnEvent(func(eventType string, data EventData) {
		if eventType == EventGiftsList {
			emitted <- parseGiftNames(data)
		}
	})

	m.handleBridgeEvent(EventGiftsList, EventData{"gifts": []interface{}{"Rose", "Dino"}})
	m.handleBridgeEvent(EventGiftsList, EventData{"gifts": []interface{}{}})

	got := m.CachedAvailableGifts()
	if len(got) != 2 || got[0] != "Rose" || got[1] != "Dino" {
		t.Fatalf("cache = %v, want [Rose Dino]", got)
	}

	select {
	case names := <-emitted:
		if len(names) != 2 {
			t.Fatalf("emitted %v", names)
		}
	case <-time.After(time.Second):
		t.Fatal("expected gifts-list event")
	}
	select {
	case names := <-emitted:
		t.Fatalf("did not expect empty gifts-list emit, got %v", names)
	default:
	}
}

func TestFetchAvailableGiftsReturnsCacheWithoutBridge(t *testing.T) {
	m, _ := New()
	m.cacheAvailableGifts([]string{"Rose", "Dino"})
	got, err := m.FetchAvailableGifts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "Rose" {
		t.Fatalf("got %v", got)
	}
}

func TestFetchAvailableGiftsWithoutBridgeReturnsEmpty(t *testing.T) {
	m, _ := New()
	got, err := m.FetchAvailableGifts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestEmitDoesNotDeadlockOnGetState(t *testing.T) {
	m, _ := New()
	m.OnEvent(func(eventType string, data EventData) {
		_ = m.GetState()
	})
	done := make(chan struct{})
	go func() {
		m.emit(EventAnyGift, EventData{"giftName": "Rose"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit deadlocked")
	}
}

func TestIsGiftCountingSettlement(t *testing.T) {
	m, _ := New()
	tests := []struct {
		name string
		data EventData
		want bool
	}{
		{"non-streak", EventData{"giftType": float64(0), "repeatEnd": false}, true},
		{"streak in progress bool", EventData{"giftType": float64(1), "repeatEnd": false}, false},
		{"streak ended bool", EventData{"giftType": float64(1), "repeatEnd": true}, true},
		{"streak in progress number", EventData{"giftType": float64(1), "repeatEnd": float64(0)}, false},
		{"streak ended number", EventData{"giftType": float64(1), "repeatEnd": float64(1)}, true},
		{"missing repeatEnd defaults settled", EventData{"giftType": float64(1)}, true},
		{"missing repeatEnd non-streak", EventData{"giftType": float64(0)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.isGiftCountingSettlement(tt.data)
			if got != tt.want {
				t.Fatalf("isGiftCountingSettlement(%v) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}
