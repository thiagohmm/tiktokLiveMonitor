package monitor

import (
	"testing"
	"time"
)

func TestSupervisorRespectsServerRetryAfter(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.SetCurrentLive("mama_voodoo777")
	defer m.Close()
	status := make(chan EventData, 1)
	m.OnEvent(func(event string, data EventData) {
		if event == EventConnectionStatus && data["retries"] != nil {
			status <- data
		}
	})
	m.startSupervisor(t.Context())
	m.handleBridgeEvent(EventConnectionStatus, EventData{
		"success": false, "retryAfterMs": float64(120000),
	})
	select {
	case data := <-status:
		if delay := data["nextRetryInMs"].(int64); delay < 119000 {
			t.Fatalf("retry scheduled in %dms, before server cooldown", delay)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not schedule retry")
	}
	// Closing cancels the long wait without launching another bridge.
	m.Close()
	m.handleBridgeEvent(EventConnectionStatus, EventData{"success": true})
	if !m.reconnectNotBefore.IsZero() {
		t.Fatal("successful connection did not clear cooldown")
	}
}
