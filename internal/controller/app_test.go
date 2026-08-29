package controller

import (
	"fmt"
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

func TestResolveGiftName(t *testing.T) {
	tests := []struct {
		name string
		data monitor.EventData
		want string
	}{
		{
			name: "flat giftName",
			data: monitor.EventData{"giftName": "Rose"},
			want: "Rosa",
		},
		{
			name: "nested giftDetails",
			data: monitor.EventData{
				"giftDetails": map[string]interface{}{"giftName": "Dino"},
			},
			want: "Dino",
		},
		{
			name: "extendedGiftInfo name",
			data: monitor.EventData{
				"extendedGiftInfo": map[string]interface{}{"name": "Perfume"},
			},
			want: "Perfume",
		},
		{
			name: "giftId fallback",
			data: monitor.EventData{"giftId": float64(5655)},
			want: "Presente 5655",
		},
		{
			name: "empty payload",
			data: monitor.EventData{},
			want: "Presente",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveGiftName(tt.data)
			if got != tt.want {
				t.Fatalf("resolveGiftName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEventIntFromJSONFloat(t *testing.T) {
	data := monitor.EventData{"repeatCount": float64(7)}
	if got := eventInt(data, "repeatCount", 1); got != 7 {
		t.Fatalf("eventInt() = %d, want 7", got)
	}
}

func TestIsGiftStreakInProgress(t *testing.T) {
	tests := []struct {
		name string
		data monitor.EventData
		want bool
	}{
		{"missing repeatEnd", monitor.EventData{}, false},
		{"repeatEnd true", monitor.EventData{"repeatEnd": true}, false},
		{"repeatEnd false", monitor.EventData{"repeatEnd": false}, true},
		{"repeatEnd 0", monitor.EventData{"repeatEnd": float64(0)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isGiftStreakInProgress(tt.data); got != tt.want {
				t.Fatalf("isGiftStreakInProgress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEventStringNilSafe(t *testing.T) {
	data := monitor.EventData{"uniqueId": nil, "userId": "abc"}
	if got := eventString(data, "uniqueId", "userId"); got != "abc" {
		t.Fatalf("eventString() = %q, want abc", got)
	}
}

func TestHandleChatMessageEventNilFieldsDoNotPanic(t *testing.T) {
	// Ensures a chat payload with JSON nulls cannot crash the process.
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("HandleChatMessageEvent panicked: %v", rec)
		}
	}()
	c := &AppController{}
	c.HandleChatMessageEvent(monitor.EventData{
		"uniqueId": nil,
		"nickname": nil,
		"comment":  nil,
	})
}

func TestEventBoolPtr(t *testing.T) {
	tests := []struct {
		name string
		data monitor.EventData
		want *bool
	}{
		{name: "missing", data: monitor.EventData{}},
		{name: "nil", data: monitor.EventData{"isFollower": nil}},
		{name: "true", data: monitor.EventData{"isFollower": true}, want: boolPtr(true)},
		{name: "false", data: monitor.EventData{"isFollower": false}, want: boolPtr(false)},
		{name: "float 1", data: monitor.EventData{"isFollower": float64(1)}, want: boolPtr(true)},
		{name: "string true", data: monitor.EventData{"isFollower": "true"}, want: boolPtr(true)},
		{name: "string 0", data: monitor.EventData{"isFollower": "0"}, want: boolPtr(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eventBoolPtr(tt.data, "isFollower")
			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("eventBoolPtr() = %v, want %v", got, *tt.want)
			}
		})
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestGetUserProfile(t *testing.T) {
	c := newTestController(t, "live1")

	// 12 distinct messages (the store keeps at most 10 per user; the
	// profile must surface the 10 most recent).
	for i := 0; i < 12; i++ {
		c.HandleChatMessageEvent(monitor.EventData{
			"uniqueId": "user1",
			"nickname": "User One",
			"comment":  fmt.Sprintf("msg %d", i),
		})
	}

	// 2 gift events: 1 + 5 units.
	c.HandleGiftEvent(giftData("user1", 1))
	c.HandleGiftEvent(giftData("user1", 5))

	// Likes: 3 + 4 = 7 (the "total" room counter is irrelevant here).
	c.HandleLikeEvent(monitor.EventData{"uniqueId": "user1", "nickname": "User One", "likeCount": 3})
	c.HandleLikeEvent(monitor.EventData{"uniqueId": "user1", "nickname": "User One", "likeCount": 4})

	// 2 shares.
	c.HandleShareEvent(monitor.EventData{"uniqueId": "user1", "nickname": "User One"})
	c.HandleShareEvent(monitor.EventData{"uniqueId": "user1", "nickname": "User One"})

	prof, err := c.GetUserProfile("user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prof.Nickname != "User One" {
		t.Fatalf("expected nickname 'User One', got %q", prof.Nickname)
	}
	if len(prof.Messages) != 10 || prof.TotalMessages != 10 {
		t.Fatalf("expected 10 messages, got %d (total %d)", len(prof.Messages), prof.TotalMessages)
	}
	// Newest first: the 12 seeded messages are FIFO-capped to the last 10.
	if prof.Messages[0].Message != "msg 11" {
		t.Fatalf("expected most recent message first, got %q", prof.Messages[0].Message)
	}
	if prof.TotalGifts != 2 {
		t.Fatalf("expected 2 gifts, got %d", prof.TotalGifts)
	}
	if prof.TotalGiftUnits != 6 {
		t.Fatalf("expected 6 gift units, got %d", prof.TotalGiftUnits)
	}
	if prof.TotalLikes != 7 {
		t.Fatalf("expected 7 likes, got %d", prof.TotalLikes)
	}
	if prof.TotalShares != 2 {
		t.Fatalf("expected 2 shares, got %d", prof.TotalShares)
	}

	// Case-insensitive lookup.
	prof, err = c.GetUserProfile("USER1")
	if err != nil {
		t.Fatalf("unexpected error (case-insensitive): %v", err)
	}
	if prof.TotalLikes != 7 || prof.TotalShares != 2 || prof.TotalGiftUnits != 6 {
		t.Fatalf("expected same totals for USER1, got likes=%d shares=%d units=%d",
			prof.TotalLikes, prof.TotalShares, prof.TotalGiftUnits)
	}

	// Unknown user: zeroed profile, no error.
	prof, err = c.GetUserProfile("nobody")
	if err != nil {
		t.Fatalf("unexpected error for unknown user: %v", err)
	}
	if prof.TotalMessages != 0 || prof.TotalGifts != 0 || prof.TotalGiftUnits != 0 ||
		prof.TotalLikes != 0 || prof.TotalShares != 0 {
		t.Fatalf("expected zeroed profile, got %+v", prof)
	}

	// Empty uid: no error, empty profile.
	prof, err = c.GetUserProfile("  ")
	if err != nil {
		t.Fatalf("unexpected error for empty uid: %v", err)
	}
	if prof.UniqueID != "  " {
		t.Fatalf("expected echoed uid, got %q", prof.UniqueID)
	}
}

func TestGetUserProfileAlertsAndRisk(t *testing.T) {
	c := newTestController(t, "live1")

	// Seed moderation alerts via the controller (persists to anomaly_logs).
	c.ReportExternalFlag(monitor.EventData{
		"uniqueId": "baduser", "nickname": "Bad", "comment": "spam one", "category": "SPAM", "reason": "SPAM",
	})
	c.ReportExternalFlag(monitor.EventData{
		"uniqueId": "baduser", "nickname": "Bad", "comment": "spam two", "category": "REPETICAO", "reason": "REPETICAO",
	})
	c.ReportExternalFlag(monitor.EventData{
		"uniqueId": "gooduser", "nickname": "Good", "comment": "ok", "category": "SPAM", "reason": "SPAM",
	})

	prof, err := c.GetUserProfile("baduser")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prof.Alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(prof.Alerts))
	}
	if prof.RiskLevel != model.RiskLevelMedium {
		t.Fatalf("expected medium risk for 2 anomalies, got %q", prof.RiskLevel)
	}

	prof, err = c.GetUserProfile("BADUSER")
	if err != nil {
		t.Fatalf("case-insensitive lookup failed: %v", err)
	}
	if len(prof.Alerts) != 2 {
		t.Fatalf("expected case-insensitive alerts, got %d", len(prof.Alerts))
	}
}
