package monitor

import (
	"testing"
)

func TestManagerNormalizesAndListsLives(t *testing.T) {
	manager := NewManager(nil, 10)
	manager.monitors["zeta"] = &Monitor{currentUsername: "zeta"}
	manager.monitors["alpha"] = &Monitor{currentUsername: "alpha"}

	states := manager.States()
	if len(states) != 2 {
		t.Fatalf("expected two lives, got %d", len(states))
	}
	if states[0].Live != "alpha" || states[1].Live != "zeta" {
		t.Fatalf("states are not sorted by live name: %#v", states)
	}
	if got := normalizeLiveName("  @creator  "); got != "creator" {
		t.Fatalf("normalizeLiveName() = %q, want creator", got)
	}
}

func TestManagerRejectsEmptyUsername(t *testing.T) {
	manager := NewManager(nil, 10)
	if err := manager.StartMonitoring(nil, "   "); err == nil {
		t.Fatal("expected empty username to be rejected")
	}
}

func TestManagerUsesConfiguredMaximum(t *testing.T) {
	manager := NewManager(nil, 3)
	if manager.max != 3 {
		t.Fatalf("manager max = %d, want 3", manager.max)
	}
	defaultManager := NewManager(nil, 0)
	if defaultManager.max != defaultMaxMonitors {
		t.Fatalf("default manager max = %d, want %d", defaultManager.max, defaultMaxMonitors)
	}
}
