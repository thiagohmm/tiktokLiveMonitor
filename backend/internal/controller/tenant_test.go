package controller

import (
	"errors"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

const otherOrgID = "org-other"

func testDB(t *testing.T, c *AppController) *database.DB {
	t.Helper()
	db, ok := c.repo.(*database.DB)
	if !ok {
		t.Fatalf("repository is %T, want *database.DB", c.repo)
	}
	return db
}

func newAttachTestController(t *testing.T) *AppController {
	t.Helper()
	c := newTestController(t, "")
	if err := testDB(t, c).ExecSQL(`INSERT INTO organizations(id,name) VALUES(?,?) ON CONFLICT DO NOTHING`, otherOrgID, "Other org"); err != nil {
		t.Fatal(err)
	}
	useFakeBridge(t)
	t.Cleanup(c.Stop)
	return c
}

func TestAttachMonitoringIsolatesOrgs(t *testing.T) {
	c := newAttachTestController(t)
	const live = "shared-live"

	if err := c.AttachMonitoring(t.Context(), testOrgID, "user-a1", live); err != nil {
		t.Fatalf("attach org-test: %v", err)
	}
	if err := c.AttachMonitoring(t.Context(), otherOrgID, "user-b1", live); err != nil {
		t.Fatalf("attach org-other: %v", err)
	}
	if err := c.AttachMonitoring(t.Context(), testOrgID, "user-a2", live); err != nil {
		t.Fatalf("attach second org-test user: %v", err)
	}
	if got := c.attachments.Watchers(testOrgID, live); got != 2 {
		t.Fatalf("org-test watchers = %d, want 2", got)
	}
	if got := c.attachments.Watchers(otherOrgID, live); got != 1 {
		t.Fatalf("org-other watchers = %d, want 1", got)
	}
	if len(c.GetLiveStates(testOrgID)) != 1 || len(c.GetLiveStates(otherOrgID)) != 1 {
		t.Fatalf("each organization must run its own monitor: a=%v b=%v",
			c.GetLiveStates(testOrgID), c.GetLiveStates(otherOrgID))
	}
	stA, _ := c.monitorManager.StateFor(testOrgID, live)
	stB, _ := c.monitorManager.StateFor(otherOrgID, live)
	if stA.LiveID == "" || stB.LiveID == "" || stA.LiveID == stB.LiveID {
		t.Fatalf("each organization must own a distinct session: a=%q b=%q", stA.LiveID, stB.LiveID)
	}

	c.DetachMonitoring(testOrgID, "user-a1", live)
	if _, ok := c.monitorManager.StateFor(testOrgID, live); !ok {
		t.Fatal("org-test monitor must keep running while a member still watches")
	}

	c.DetachMonitoring(testOrgID, "user-a2", "")
	if _, ok := c.monitorManager.StateFor(testOrgID, live); ok {
		t.Fatal("org-test monitor must stop after its last watcher left")
	}
	if _, ok := c.monitorManager.StateFor(otherOrgID, live); !ok {
		t.Fatal("org-other monitor of the same live must keep running")
	}

	c.StopOrgMonitoring(otherOrgID)
	if len(c.GetLiveStates(otherOrgID)) != 0 || c.attachments.OrgWatchers(otherOrgID) != 0 {
		t.Fatal("StopOrgMonitoring must stop the lives and forget the watchers of the organization")
	}
}

func TestAttachMonitoringRespectsOrgLimitAndStatus(t *testing.T) {
	c := newAttachTestController(t)
	db := testDB(t, c)
	org, err := db.CreateOrganization("Limitada", 1)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	if err := c.AttachMonitoring(t.Context(), org.ID, "user-1", "live-one"); err != nil {
		t.Fatalf("first live: %v", err)
	}
	err = c.AttachMonitoring(t.Context(), org.ID, "user-1", "live-two")
	if !errors.Is(err, monitor.ErrOrgLiveLimit) {
		t.Fatalf("expected ErrOrgLiveLimit, got %v", err)
	}
	if got := c.attachments.Watchers(org.ID, "live-two"); got != 0 {
		t.Fatalf("a rejected start must roll back the attachment, got %d", got)
	}
	// The quota of one organization does not affect another.
	if err := c.AttachMonitoring(t.Context(), testOrgID, "user-x", "live-two"); err != nil {
		t.Fatalf("another organization must not be limited: %v", err)
	}

	inactive := false
	if _, err := db.UpdateOrganization(org.ID, nil, nil, &inactive); err != nil {
		t.Fatalf("disable organization: %v", err)
	}
	c.StopOrgMonitoring(org.ID)
	if err := c.AttachMonitoring(t.Context(), org.ID, "user-1", "live-one"); err == nil {
		t.Fatal("a disabled organization must not start lives")
	}
	if got := c.attachments.Watchers(org.ID, "live-one"); got != 0 {
		t.Fatalf("a rejected start must roll back the attachment, got %d", got)
	}
}

func TestEventsWithoutOrgAreIgnored(t *testing.T) {
	c := newTestController(t, testLive)

	c.HandleGiftEvent(monitor.EventData{
		"liveName": testLive, "uniqueId": "u1", "giftName": "Rose", "repeatCount": 3, "repeatEnd": true,
	})
	c.HandleChatMessageEvent(monitor.EventData{"liveName": testLive, "uniqueId": "u1", "comment": "oi"})
	c.HandleLikeEvent(monitor.EventData{"liveName": testLive, "uniqueId": "u1", "likeCount": 5})
	c.HandleShareEvent(monitor.EventData{"liveName": testLive, "uniqueId": "u1"})
	c.ReportExternalFlag(monitor.EventData{
		"liveName": testLive, "uniqueId": "u1", "comment": "spam", "category": "SPAM",
	})
	if _, err := c.RecordPinnedComment(monitor.EventData{"liveName": testLive, "uniqueId": "u1", "comment": "fix"}); !errors.Is(err, model.ErrOrgRequired) {
		t.Fatalf("pinned comment without org: got %v, want ErrOrgRequired", err)
	}
	if _, err := c.RecordTargetGiftReceived(monitor.EventData{"liveName": testLive, "uniqueId": "u1", "giftName": "Rose"}); !errors.Is(err, model.ErrOrgRequired) {
		t.Fatalf("target gift without org: got %v, want ErrOrgRequired", err)
	}

	prof, err := c.GetUserProfile(testOrgID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if prof.TotalGifts != 0 || prof.TotalMessages != 0 || prof.TotalLikes != 0 || prof.TotalShares != 0 || len(prof.Alerts) != 0 {
		t.Fatalf("events without orgId must not be persisted: %+v", prof)
	}
}

func TestEventsOfAnotherOrgDoNotReachSession(t *testing.T) {
	c := newTestController(t, testLive)

	// org-other has no session for the live: its event cannot be written into
	// org-test's session of the same streamer.
	c.HandleGiftEvent(monitor.EventData{
		"orgId": otherOrgID, "liveName": testLive, "uniqueId": "u1", "giftName": "Rose", "repeatCount": 3, "repeatEnd": true,
	})
	gifts, err := c.GetRecentGifts(testOrgID, testLive, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(gifts) != 0 {
		t.Fatalf("org-other event leaked into org-test: %+v", gifts)
	}

	c.HandleGiftEvent(giftData("u1", 2))
	if gifts, _ := c.GetRecentGifts(testOrgID, testLive, 10); len(gifts) != 1 {
		t.Fatalf("org-test gift must be stored, got %d", len(gifts))
	}
	if gifts, _ := c.GetRecentGifts(otherOrgID, testLive, 10); len(gifts) != 0 {
		t.Fatalf("org-other must not see org-test gifts, got %d", len(gifts))
	}
	if prof, _ := c.GetUserProfile(otherOrgID, "u1"); prof.TotalGifts != 0 {
		t.Fatalf("org-other profile leaked org-test gifts: %+v", prof)
	}
}

func TestGoalsAreIsolatedPerOrg(t *testing.T) {
	c := newTestController(t, testLive)
	db := testDB(t, c)
	if _, err := db.BeginLiveSession(otherOrgID, testLive, time.Now()); err != nil {
		t.Fatalf("begin org-other session: %v", err)
	}

	var updates []GoalUpdate
	c.SetGoalCallback(func(u GoalUpdate) { updates = append(updates, u) })

	goal, err := c.CreateGoal(testOrgID, testLive, "meta", "", 10, nil)
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	other, err := c.GetGoalsState(otherOrgID, testLive)
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Actives) != 0 || len(other.History) != 0 {
		t.Fatalf("org-other must not see org-test goals: %+v", other)
	}
	if err := c.CancelGoal(otherOrgID, testLive, goal.ID); err == nil {
		t.Fatal("org-other must not cancel org-test's goal")
	}
	if err := c.CompleteGoal(otherOrgID, testLive, goal.ID); err == nil {
		t.Fatal("org-other must not complete org-test's goal")
	}

	// Gifts of org-other's session of the same streamer do not count.
	c.HandleGiftEvent(monitor.EventData{
		"orgId": otherOrgID, "liveName": testLive, "uniqueId": "u1", "giftName": "Rose", "repeatCount": 50, "repeatEnd": true,
	})
	if len(updates) != 0 {
		t.Fatalf("org-other gifts must not move org-test goals: %+v", updates)
	}
	st, err := c.GetGoalsState(testOrgID, testLive)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Actives) != 1 || st.Actives[0].Units != 0 {
		t.Fatalf("org-test goal must be untouched: %+v", st)
	}

	c.HandleGiftEvent(giftData("u1", 4))
	if len(updates) != 1 || updates[0].OrgID != testOrgID {
		t.Fatalf("goal update must carry org-test, got %+v", updates)
	}
}

func TestLiveSessionAndTargetGiftsScopedToOrg(t *testing.T) {
	c := newTestController(t, testLive)
	ref := c.activeRefForTest(t)

	if _, err := c.GetLiveSession(testOrgID, ref.ID); err != nil {
		t.Fatalf("own session: %v", err)
	}
	if _, err := c.GetLiveSession(otherOrgID, ref.ID); !errors.Is(err, model.ErrLiveSessionNotFound) {
		t.Fatalf("other org session lookup: got %v, want ErrLiveSessionNotFound", err)
	}
	if _, err := c.DeleteLive(otherOrgID, ref.ID); !errors.Is(err, model.ErrLiveSessionNotFound) {
		t.Fatalf("other org delete: got %v, want ErrLiveSessionNotFound", err)
	}

	id, err := c.RecordTargetGiftReceived(liveEvent(monitor.EventData{"uniqueId": "u1", "giftName": "Rose"}))
	if err != nil {
		t.Fatalf("record target gift: %v", err)
	}
	if err := c.AnswerTargetGift(otherOrgID, id, model.TargetGiftResponseManual); !errors.Is(err, model.ErrInvalidID) {
		t.Fatalf("other org answer: got %v, want ErrInvalidID", err)
	}
	if pending, _ := c.GetPendingTargetGiftHistory(otherOrgID, testLive, 10); len(pending) != 0 {
		t.Fatalf("org-other must not see org-test's queue: %+v", pending)
	}
	if err := c.AnswerTargetGift(testOrgID, id, model.TargetGiftResponseManual); err != nil {
		t.Fatalf("own answer: %v", err)
	}
	if _, err := c.GetLiveSession(testOrgID, ref.ID); err != nil {
		t.Fatalf("the session must survive the rejected delete: %v", err)
	}
}
