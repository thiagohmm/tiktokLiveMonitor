package monitor

import (
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
)

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

func TestSessionReusable(t *testing.T) {
	now := time.Date(2026, 8, 17, 15, 0, 0, 0, time.Local)
	tests := []struct {
		name string
		last time.Time
		want bool
	}{
		{"zero", time.Time{}, false},
		{"same day under 10h", now.Add(-2 * time.Hour), true},
		{"same day exactly 10h", now.Add(-10 * time.Hour), false},
		{"same day over 10h", now.Add(-11 * time.Hour), false},
		{"previous day under 10h", now.Add(-16 * time.Hour), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionReusable(tt.last, now)
			if got != tt.want {
				t.Fatalf("sessionReusable(%v) = %v, want %v", tt.last, got, tt.want)
			}
		})
	}
}

func newMonitorWithDB(t *testing.T) (*Monitor, *database.DB) {
	t.Helper()
	db, err := database.Open(t.TempDir())
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

func TestRestoreOrPurgeKeepsRecentSameDay(t *testing.T) {
	m, db := newMonitorWithDB(t)

	if err := db.AddUserMessageDedup("user1", "User One", "hello today"); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if err := db.LogAnomaly("live1", "spam", true, "SPAM", "user1"); err != nil {
		t.Fatalf("log anomaly: %v", err)
	}
	if _, err := db.AddGift("live1", "user1", "User One", "Rose", 1, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}

	m.restoreOrPurgeSessionData()

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

func TestRestoreOrPurgeDeletesStaleSession(t *testing.T) {
	m, db := newMonitorWithDB(t)

	stale := time.Now().Add(-25 * time.Hour).Format("2006-01-02 15:04:05")
	err := db.ExecSQL(
		`INSERT INTO gifts (live_name, uniqueId, nickname, gift_name, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"live1", "user1", "User", "Rose", stale,
	)
	if err != nil {
		t.Fatalf("insert gift: %v", err)
	}
	err = db.ExecSQL(
		`INSERT INTO anomaly_logs (live_name, day, uniqueId, comment, is_anomaly, category, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"live1", "2020-01-01", "user1", "old", 1, "SPAM", stale,
	)
	if err != nil {
		t.Fatalf("insert anomaly: %v", err)
	}
	err = db.ExecSQL(
		`INSERT INTO user_messages (uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?)`,
		"user1", "User", "old hello", stale,
	)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}

	m.restoreOrPurgeSessionData()

	if len(m.GetChatBuffer()) != 0 {
		t.Fatal("expected empty chat buffer after purge")
	}
	if m.IsPinnedUser("user1") {
		t.Fatal("expected no pinned users after purge")
	}
	gifts, err := db.GetRecentGifts("live1", 10)
	if err != nil {
		t.Fatalf("gifts: %v", err)
	}
	if len(gifts) != 0 {
		t.Fatalf("expected gifts deleted, got %d", len(gifts))
	}
	msgs, err := db.GetTodayUserMessages()
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected messages deleted, got %d", len(msgs))
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
