package monitor

import (
	"sync"
	"testing"
	"time"
)

func TestChooseQuestionHeuristicSameUser(t *testing.T) {
	gift := GiftPayload{UniqueID: "alice", Nickname: "Alice", GiftName: "Rosa"}
	questions := []QuestionEntry{
		{UniqueID: "bob", Nickname: "Bob", Comment: "como vai?", Timestamp: 1},
		{UniqueID: "alice", Nickname: "Alice", Comment: "qual sua religiao?", Timestamp: 2},
	}
	pick := chooseQuestionHeuristic(gift, questions, nil)
	if pick == nil {
		t.Fatal("expected heuristic match")
	}
	if pick.method != "same-user-question" {
		t.Fatalf("method = %q, want same-user-question", pick.method)
	}
	if pick.match.Comment != "qual sua religiao?" {
		t.Fatalf("comment = %q", pick.match.Comment)
	}
}

func TestChooseQuestionHeuristicRecentMessage(t *testing.T) {
	gift := GiftPayload{UniqueID: "alice", Nickname: "Alice", GiftName: "Rosa"}
	recent := []QuestionEntry{
		{UniqueID: "alice", Nickname: "Alice", Comment: "oi galera", Timestamp: 1},
	}
	pick := chooseQuestionHeuristic(gift, nil, recent)
	if pick == nil || pick.method != "same-user-recent-message" {
		t.Fatalf("got %+v, want same-user-recent-message", pick)
	}
}

func TestChooseQuestionHeuristicNicknameMention(t *testing.T) {
	gift := GiftPayload{UniqueID: "user9", Nickname: "Thiago", GiftName: "Perfume"}
	questions := []QuestionEntry{
		{UniqueID: "viewer", Nickname: "Viewer", Comment: "thiago qual o horario?", Timestamp: 1},
	}
	pick := chooseQuestionHeuristic(gift, questions, nil)
	if pick == nil || pick.method != "nickname-mention" {
		t.Fatalf("got %+v, want nickname-mention", pick)
	}
}

func TestChooseQuestionHeuristicNoMatch(t *testing.T) {
	gift := GiftPayload{UniqueID: "alice", Nickname: "Alice", GiftName: "Rosa"}
	if pick := chooseQuestionHeuristic(gift, nil, nil); pick != nil {
		t.Fatalf("expected nil, got %+v", pick)
	}
}

func TestCorrelateGiftWithQuestionEmits(t *testing.T) {
	m, _ := New()
	m.SetSettings(Settings{TargetGifts: []string{"Rosa"}})

	var mu sync.Mutex
	var got EventData
	m.OnEvent(func(eventType string, data EventData) {
		if eventType == EventGiftQuestionCorr {
			mu.Lock()
			got = data
			mu.Unlock()
		}
	})

	m.handleBridgeEvent("new-chat-message", EventData{
		"uniqueId": "alice",
		"nickname": "Alice",
		"comment":  "qual sua religiao?",
	})
	m.correlateGiftWithQuestion(GiftPayload{
		GiftName: "Rosa",
		UniqueID: "alice",
		Nickname: "Alice",
	})

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("expected gift-question-correlation event")
	}
	if got["question"] != "qual sua religiao?" {
		t.Fatalf("question = %v", got["question"])
	}
	if got["method"] != "same-user-question" {
		t.Fatalf("method = %v", got["method"])
	}
}

func TestHandleTargetGiftStartsCorrelation(t *testing.T) {
	m, _ := New()
	m.SetSettings(Settings{TargetGifts: []string{"Rosa"}})

	done := make(chan EventData, 1)
	m.OnEvent(func(eventType string, data EventData) {
		if eventType == EventGiftQuestionCorr {
			select {
			case done <- data:
			default:
			}
		}
	})

	m.handleBridgeEvent("new-chat-message", EventData{
		"uniqueId": "alice",
		"nickname": "Alice",
		"comment":  "como vai?",
	})
	m.handleBridgeEvent("new-gift-user", EventData{
		"uniqueId": "alice",
		"nickname": "Alice",
		"giftName": "Rosa",
		"giftType": float64(0),
	})

	select {
	case data := <-done:
		if data["giftName"] != "Rosa" {
			t.Fatalf("giftName = %v", data["giftName"])
		}
	case <-time.After(time.Second):
		t.Fatal("expected correlation after target gift")
	}
}

func TestScoreCorrelationCandidate(t *testing.T) {
	if scoreCorrelationCandidate(QuestionEntry{Comment: "oi"}) >= scoreCorrelationCandidate(QuestionEntry{Comment: "qual sua religiao?"}) {
		t.Fatal("expected question to score higher than greeting")
	}
}

func TestGetForwardMessagesPrefersSameAuthor(t *testing.T) {
	base := QuestionEntry{UniqueID: "alice", Nickname: "Alice", Comment: "oi", Timestamp: 10}
	chat := []ChatMessage{
		{UniqueID: "bob", Nickname: "Bob", Comment: "como assim?", Timestamp: 11},
		{UniqueID: "alice", Nickname: "Alice", Comment: "qual o horario?", Timestamp: 12},
		{UniqueID: "alice", Nickname: "Alice", Comment: "ainda esta ai?", Timestamp: 13},
	}
	got := getForwardMessages(base, chat, 2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Comment != "qual o horario?" || got[1].Comment != "ainda esta ai?" {
		t.Fatalf("unexpected forward messages: %+v", got)
	}
}
