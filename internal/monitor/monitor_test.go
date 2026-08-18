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
