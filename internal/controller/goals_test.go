package controller

import (
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

func newTestController(t *testing.T, liveName string) *AppController {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mon, err := monitor.New()
	if err != nil {
		t.Fatalf("new monitor: %v", err)
	}
	mon.SetCurrentLive(liveName)

	c := NewAppController(mon, db)
	return c
}

func giftData(uniqueID string, repeatCount int) monitor.EventData {
	return monitor.EventData{
		"uniqueId":    uniqueID,
		"nickname":    "User One",
		"giftName":    "Rose",
		"repeatCount": repeatCount,
		"repeatEnd":   true,
	}
}

func TestCreateGoalAndState(t *testing.T) {
	c := newTestController(t, "live1")

	g, err := c.CreateGoal("Meta da noite", "", 100, []model.GoalMilestone{
		{AtUnits: 50, Reward: "música especial"},
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if g.ID <= 0 || g.Status != model.GoalStatusActive {
		t.Fatalf("unexpected goal: %+v", g)
	}

	st, err := c.GetGoalsState()
	if err != nil {
		t.Fatalf("get goals state: %v", err)
	}
	if st.LiveName != "live1" {
		t.Fatalf("expected live1, got %q", st.LiveName)
	}
	if st.Active == nil {
		t.Fatal("expected an active goal")
	}
	if st.Active.Units != 0 || st.Active.Percent != 0 {
		t.Fatalf("expected 0 units/0%%, got %d/%.1f", st.Active.Units, st.Active.Percent)
	}
	if len(st.History) != 0 {
		t.Fatalf("expected empty history, got %d", len(st.History))
	}
}

func TestCreateGoalCancelsPreviousActive(t *testing.T) {
	c := newTestController(t, "live1")

	if _, err := c.CreateGoal("meta antiga", "", 10, nil); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if _, err := c.CreateGoal("meta nova", "", 20, nil); err != nil {
		t.Fatalf("create second: %v", err)
	}

	st, err := c.GetGoalsState()
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st.Active == nil || st.Active.Goal.Title != "meta nova" {
		t.Fatalf("expected new active goal, got %+v", st.Active)
	}
	if len(st.History) != 1 || st.History[0].Status != model.GoalStatusCancelled {
		t.Fatalf("expected cancelled previous goal, got %+v", st.History)
	}
}

func TestGoalProgressFlow(t *testing.T) {
	c := newTestController(t, "live1")

	var updates []GoalUpdate
	c.SetGoalCallback(func(u GoalUpdate) { updates = append(updates, u) })

	if _, err := c.CreateGoal("Meta", "", 100, []model.GoalMilestone{
		{AtUnits: 50, Reward: "música especial"},
		{AtUnits: 80, Reward: "dedicatória"},
	}); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	// First wave: 60 units crosses only the 50 milestone.
	c.HandleGiftEvent(giftData("u1", 60))
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	u := updates[0]
	if u.Completed {
		t.Fatal("goal should not be completed yet")
	}
	if len(u.UnlockedMilestones) != 1 || u.UnlockedMilestones[0].Reward != "música especial" {
		t.Fatalf("unexpected unlocked: %+v", u.UnlockedMilestones)
	}
	if u.Progress.Units != 60 || u.Progress.Percent != 60 {
		t.Fatalf("expected 60 units / 60%%, got %d / %.1f", u.Progress.Units, u.Progress.Percent)
	}

	// No progress event without gifts: a duplicate check must be silent.
	c.checkGoalProgress()
	if len(updates) != 1 {
		t.Fatalf("expected no new update, got %d", len(updates))
	}

	// Second wave: +50 reaches 110 >= 100 -> completes the goal.
	c.HandleGiftEvent(giftData("u2", 50))
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	u = updates[1]
	if !u.Completed {
		t.Fatal("expected goal completed")
	}
	if u.Progress.Units != 110 || u.Progress.Percent != 100 {
		t.Fatalf("expected 110 units / 100%%, got %d / %.1f", u.Progress.Units, u.Progress.Percent)
	}
	if len(u.UnlockedMilestones) != 2 {
		t.Fatalf("expected 2 unlocked milestones, got %d", len(u.UnlockedMilestones))
	}

	// Persisted state reflects completion.
	st, err := c.GetGoalsState()
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st.Active != nil {
		t.Fatalf("expected no active goal after completion, got %+v", st.Active)
	}
	if len(st.History) != 1 || st.History[0].Status != model.GoalStatusCompleted || st.History[0].CompletedAt == nil {
		t.Fatalf("expected completed goal in history, got %+v", st.History)
	}

	// Gifts after completion must not re-emit.
	c.HandleGiftEvent(giftData("u3", 10))
	if len(updates) != 2 {
		t.Fatalf("expected no update after completion, got %d", len(updates))
	}
}

func TestPerGiftGoal(t *testing.T) {
	c := newTestController(t, "live1")

	if _, err := c.CreateGoal("Meta de Rose", "Rose", 10, []model.GoalMilestone{
		{AtUnits: 5, Reward: "música especial"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Gifts other than the goal's gift must not count.
	c.HandleGiftEvent(monitor.EventData{
		"uniqueId": "u1", "nickname": "User One", "giftName": "Dino", "repeatCount": 40, "repeatEnd": true,
	})
	st, err := c.GetGoalsState()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.Active == nil {
		t.Fatal("expected active goal")
	}
	if st.Active.Units != 0 || st.Active.Percent != 0 {
		t.Fatalf("expected 0 units / 0%%, got %d / %.1f", st.Active.Units, st.Active.Percent)
	}

	// 5 roses unlock the 5-unit milestone.
	c.HandleGiftEvent(monitor.EventData{
		"uniqueId": "u2", "nickname": "User Two", "giftName": "Rose", "repeatCount": 5, "repeatEnd": true,
	})
	st, err = c.GetGoalsState()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.Active == nil || st.Active.Units != 5 || st.Active.Percent != 50 {
		t.Fatalf("expected 5 units / 50%%, got %+v", st.Active)
	}
	if !st.Active.Goal.Milestones[0].Unlocked {
		t.Fatal("expected 5-unit milestone unlocked")
	}

	// 6 more roses reach 11 >= 10 and complete the goal.
	c.HandleGiftEvent(monitor.EventData{
		"uniqueId": "u3", "nickname": "User Three", "giftName": "Rose", "repeatCount": 6, "repeatEnd": true,
	})
	st, err = c.GetGoalsState()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.Active != nil {
		t.Fatalf("expected completed goal, active: %+v", st.Active)
	}
	if len(st.History) != 1 || st.History[0].Status != model.GoalStatusCompleted || st.History[0].GiftName != "Rose" {
		t.Fatalf("expected completed per-gift goal in history, got %+v", st.History)
	}
}

// TestGoalProgressEmitsOnPlainGifts ensures the goal callback fires for gifts
// that only move the progress bar (no milestone crossed, no completion),
// so the UI percent/bar updates on every gift received.
func TestGoalProgressEmitsOnPlainGifts(t *testing.T) {
	c := newTestController(t, "live1")

	var updates []GoalUpdate
	c.SetGoalCallback(func(u GoalUpdate) { updates = append(updates, u) })

	if _, err := c.CreateGoal("Meta", "", 1000, nil); err != nil {
		t.Fatalf("create goal: %v", err)
	}

	// No milestones: plain gifts must still emit progress updates.
	c.HandleGiftEvent(giftData("u1", 10))
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Progress.Units != 10 || updates[0].Progress.Percent != 1 {
		t.Fatalf("expected 10 units / 1%%, got %d / %.2f", updates[0].Progress.Units, updates[0].Progress.Percent)
	}

	c.HandleGiftEvent(giftData("u2", 15))
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	if updates[1].Progress.Units != 25 || updates[1].Progress.Percent != 2.5 {
		t.Fatalf("expected 25 units / 2.5%%, got %d / %.2f", updates[1].Progress.Units, updates[1].Progress.Percent)
	}

	// A repeated check with unchanged units must stay silent.
	c.checkGoalProgress()
	if len(updates) != 2 {
		t.Fatalf("expected no new update on unchanged units, got %d", len(updates))
	}

	// Completed goals must not re-emit on later gifts.
	c.CompleteGoal()
	before := len(updates)
	c.HandleGiftEvent(giftData("u3", 5))
	if len(updates) != before {
		t.Fatalf("expected no update after completion, got %d", len(updates))
	}
}

func TestCancelGoal(t *testing.T) {
	c := newTestController(t, "live1")

	if _, err := c.CreateGoal("meta", "", 100, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.CancelGoal(); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	st, err := c.GetGoalsState()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.Active != nil || len(st.History) != 1 || st.History[0].Status != model.GoalStatusCancelled {
		t.Fatalf("unexpected state after cancel: %+v", st)
	}
	if err := c.CancelGoal(); err == nil {
		t.Fatal("expected error cancelling with no active goal")
	}
}

func TestCompleteGoalManual(t *testing.T) {
	c := newTestController(t, "live1")

	if _, err := c.CreateGoal("meta", "", 100, []model.GoalMilestone{{AtUnits: 30, Reward: "prêmio"}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	c.HandleGiftEvent(giftData("u1", 30))

	if err := c.CompleteGoal(); err != nil {
		t.Fatalf("complete: %v", err)
	}
	st, err := c.GetGoalsState()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if len(st.History) != 1 || st.History[0].Status != model.GoalStatusCompleted {
		t.Fatalf("expected completed goal, got %+v", st.History)
	}
	if !st.History[0].Milestones[0].Unlocked || st.History[0].Milestones[0].UnlockedAt == nil {
		t.Fatalf("expected crossed milestone unlocked, got %+v", st.History[0].Milestones[0])
	}
	if err := c.CompleteGoal(); err == nil {
		t.Fatal("expected error completing with no active goal")
	}
}

func TestUpdateGoal(t *testing.T) {
	c := newTestController(t, "live1")

	g, err := c.CreateGoal("meta", "", 100, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	g.Title = "meta editada"
	g.TargetUnits = 200
	if err := c.UpdateGoal(g); err != nil {
		t.Fatalf("update: %v", err)
	}
	st, err := c.GetGoalsState()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.Active == nil || st.Active.Goal.Title != "meta editada" || st.Active.Goal.TargetUnits != 200 {
		t.Fatalf("unexpected goal after update: %+v", st.Active)
	}
	if err := c.UpdateGoal(model.GiftGoal{ID: 0}); err == nil {
		t.Fatal("expected error updating invalid goal")
	}
}

func TestCreateGoalWithoutLive(t *testing.T) {
	c := newTestController(t, "")
	if _, err := c.CreateGoal("meta", "", 10, nil); err == nil {
		t.Fatal("expected error when no live is monitored")
	}
}
