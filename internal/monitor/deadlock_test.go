package monitor

import (
	"testing"
	"time"
)

// TestNoDeadlockOnKeywordChat reproduz o cenário que travava o app:
// mensagem de chat contendo palavra-alvo ("perfume") disparava emit()
// com o mutex travado -> deadlock do readBridge.
func TestNoDeadlockOnKeywordChat(t *testing.T) {
	m, _ := New()
	m.OnEvent(func(eventType string, data EventData) {})

	done := make(chan struct{})
	go func() {
		defer close(done)
		// keyword path (detectKeyword -> pinnedUsers + 2 emits)
		m.handleBridgeEvent("new-chat-message", EventData{
			"uniqueId": "user1", "nickname": "User1", "comment": "quero perfume",
		})
		// repeat path (3x mesma mensagem -> flagged-message emit)
		for i := 0; i < 3; i++ {
			m.handleBridgeEvent("new-chat-message", EventData{
				"uniqueId": "user2", "nickname": "User2", "comment": "oi oi oi",
			})
		}
	}()

	select {
	case <-done:
		// OK - sem deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: handleBridgeEvent travou em mensagem com keyword/repeat")
	}
}
