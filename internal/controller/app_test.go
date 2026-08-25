package controller

import (
	"testing"

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
