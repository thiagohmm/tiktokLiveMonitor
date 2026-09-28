package monitor

import (
	"errors"
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func addTestMonitor(t *testing.T, manager *Manager, orgID, live string) *Monitor {
	t.Helper()
	m, err := New()
	if err != nil {
		t.Fatalf("new monitor: %v", err)
	}
	m.SetOrgID(orgID)
	m.SetCurrentLive(live)
	manager.mu.Lock()
	manager.monitors[liveKey{org: orgID, live: live}] = m
	manager.mu.Unlock()
	return m
}

func TestManagerNormalizesAndListsLives(t *testing.T) {
	manager := NewManager(nil, 10)
	manager.monitors[liveKey{org: "org-a", live: "zeta"}] = &Monitor{currentUsername: "zeta"}
	manager.monitors[liveKey{org: "org-a", live: "alpha"}] = &Monitor{currentUsername: "alpha"}

	states := manager.States("org-a")
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
	if err := manager.StartMonitoring(t.Context(), "org-a", "   ", Settings{}, 0); err == nil {
		t.Fatal("expected empty username to be rejected")
	}
}

func TestManagerRejectsEmptyOrg(t *testing.T) {
	manager := NewManager(nil, 10)
	err := manager.StartMonitoring(t.Context(), "  ", "creator", Settings{}, 0)
	if !errors.Is(err, model.ErrOrgRequired) {
		t.Fatalf("expected ErrOrgRequired, got %v", err)
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

func TestManagerSameLiveInTwoOrgsIsIndependent(t *testing.T) {
	manager := NewManager(nil, 10)
	monA := addTestMonitor(t, manager, "org-a", "creator")
	monB := addTestMonitor(t, manager, "org-b", "creator")
	if monA == monB {
		t.Fatal("expected one monitor per organization")
	}

	manager.SetSettings("org-a", Settings{TargetGifts: []string{"Rose"}})
	stA, okA := manager.StateFor("org-a", "@creator")
	stB, okB := manager.StateFor("org-b", "creator")
	if !okA || !okB {
		t.Fatalf("expected both organizations to monitor the live (a=%v b=%v)", okA, okB)
	}
	if len(stA.Settings.TargetGifts) != 1 || len(stB.Settings.TargetGifts) != 0 {
		t.Fatalf("settings leaked between organizations: a=%v b=%v", stA.Settings.TargetGifts, stB.Settings.TargetGifts)
	}

	manager.StopMonitoring("org-a", "creator")
	if _, ok := manager.StateFor("org-a", "creator"); ok {
		t.Fatal("org-a monitor should be stopped")
	}
	if _, ok := manager.StateFor("org-b", "creator"); !ok {
		t.Fatal("stopping org-a must not stop org-b's monitor of the same live")
	}
}

func TestManagerOrgLiveLimitIsPerOrg(t *testing.T) {
	manager := NewManager(nil, 10)
	addTestMonitor(t, manager, "org-a", "one")
	addTestMonitor(t, manager, "org-a", "two")
	addTestMonitor(t, manager, "org-b", "one")

	err := manager.StartMonitoring(t.Context(), "org-a", "three", Settings{}, 2)
	if !errors.Is(err, ErrOrgLiveLimit) {
		t.Fatalf("expected ErrOrgLiveLimit for org-a, got %v", err)
	}
	if got := manager.countOrgLocked("org-b"); got != 1 {
		t.Fatalf("org-b count = %d, want 1", got)
	}
	if got := len(manager.monitors); got != 3 {
		t.Fatalf("rejected start must not register a monitor, got %d", got)
	}
}

func TestManagerGlobalLimitAppliesBeforeOrgLimit(t *testing.T) {
	manager := NewManager(nil, 2)
	addTestMonitor(t, manager, "org-a", "one")
	addTestMonitor(t, manager, "org-b", "one")

	err := manager.StartMonitoring(t.Context(), "org-c", "one", Settings{}, 5)
	if err == nil || errors.Is(err, ErrOrgLiveLimit) {
		t.Fatalf("expected global maximum error, got %v", err)
	}
}

func TestManagerStopAllLivesOfOrgKeepsOtherOrgs(t *testing.T) {
	manager := NewManager(nil, 10)
	addTestMonitor(t, manager, "org-a", "one")
	addTestMonitor(t, manager, "org-a", "two")
	addTestMonitor(t, manager, "org-b", "one")

	manager.StopMonitoring("org-a", "")
	if got := manager.States("org-a"); len(got) != 0 {
		t.Fatalf("expected no lives for org-a, got %#v", got)
	}
	if got := manager.States("org-b"); len(got) != 1 || got[0].Live != "one" {
		t.Fatalf("org-b lives must survive, got %#v", got)
	}

	manager.StopMonitoring("", "")
	if got := manager.States("org-b"); len(got) != 1 {
		t.Fatalf("empty org must be a no-op, got %#v", got)
	}

	manager.StopAll()
	if len(manager.monitors) != 0 {
		t.Fatalf("StopAll left %d monitors", len(manager.monitors))
	}
}

func TestManagerStatesFilteredByOrg(t *testing.T) {
	manager := NewManager(nil, 10)
	addTestMonitor(t, manager, "org-a", "alpha")
	addTestMonitor(t, manager, "org-b", "beta")
	addTestMonitor(t, manager, "org-b", "gamma")

	a := manager.States("org-a")
	if len(a) != 1 || a[0].Live != "alpha" {
		t.Fatalf("org-a states = %#v", a)
	}
	b := manager.States("org-b")
	if len(b) != 2 || b[0].Live != "beta" || b[1].Live != "gamma" {
		t.Fatalf("org-b states = %#v", b)
	}
	if got := manager.States("org-c"); len(got) != 0 {
		t.Fatalf("unknown org must see nothing, got %#v", got)
	}
	if got := manager.CurrentState("org-b").Username; got != "beta" {
		t.Fatalf("CurrentState(org-b).Username = %q, want beta", got)
	}
	if got := manager.CurrentState("org-c"); got.Username != "" {
		t.Fatalf("CurrentState(org-c) = %#v, want zero", got)
	}
	if _, err := manager.FetchAvailableGifts("org-a", "beta"); err == nil {
		t.Fatal("org-a must not reach org-b's monitor")
	}
}
