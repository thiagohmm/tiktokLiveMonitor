package controller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

func newManagedTestController(t *testing.T) *AppController {
	t.Helper()
	c := newTestController(t, "legacy-live")
	// Keep the real manager and monitor lifecycle, replacing only the external
	// TikTok bridge with a local process that accepts commands without networking.
	dir := t.TempDir()
	bridgeDir := filepath.Join(dir, "internal", "monitor")
	if err := os.MkdirAll(bridgeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.js"), []byte("process.stdin.resume();\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	manager := monitor.NewManager(c.repo, 10)
	manager.SetSettings(c.GetSettings())
	c.SetMonitorManager(manager)
	t.Cleanup(c.Stop)
	if err := c.StartMonitoring(t.Context(), "managed-live"); err != nil {
		t.Fatalf("start managed live: %v", err)
	}
	return c
}

func TestManagedLiveGoalLifecycle(t *testing.T) {
	for _, status := range []string{model.GoalStatusCancelled, model.GoalStatusCompleted} {
		t.Run(status, func(t *testing.T) {
			c := newManagedTestController(t)
			goal, err := c.CreateGoal("Managed goal", "", 100, nil)
			if err != nil {
				t.Fatal(err)
			}
			if goal.LiveName != "managed-live" {
				t.Fatalf("goal stored for %q", goal.LiveName)
			}
			gift := giftData("viewer", 5)
			gift["liveName"] = "managed-live"
			c.HandleGiftEvent(gift)
			state, err := c.GetGoalsState()
			if err != nil {
				t.Fatal(err)
			}
			if state.LiveName != "managed-live" || len(state.Actives) != 1 || state.Actives[0].Units != 5 {
				t.Fatalf("unexpected managed goal state: %+v", state)
			}
			if status == model.GoalStatusCancelled {
				err = c.CancelGoal(goal.ID)
			} else {
				err = c.CompleteGoal(goal.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			state, err = c.GetGoalsState()
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Actives) != 0 || len(state.History) != 1 || state.History[0].Status != status {
				t.Fatalf("unexpected goal history: %+v", state)
			}
		})
	}
}

func TestManagedLivePinnedComments(t *testing.T) {
	c := newManagedTestController(t)
	for _, live := range []string{"legacy-live", "managed-live"} {
		if _, err := c.RecordPinnedComment(monitor.EventData{
			"liveName": live, "uniqueId": "viewer", "comment": live, "pinId": live,
		}); err != nil {
			t.Fatal(err)
		}
	}
	comments, err := c.GetRecentPinnedComments(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].LiveName != "managed-live" {
		t.Fatalf("expected only managed live comments, got %+v", comments)
	}
}
