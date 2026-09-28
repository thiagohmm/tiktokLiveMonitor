package database

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// newTestOrg creates an organization for tenant-isolation tests.
func newTestOrg(t *testing.T, db *DB, name string) string {
	t.Helper()
	o, err := db.CreateOrganization(name, 0)
	if err != nil {
		t.Fatalf("create organization %q: %v", name, err)
	}
	return o.ID
}

// twoOrgSessions opens a session of the SAME streamer in two organizations.
func twoOrgSessions(t *testing.T, db *DB, liveName string) (orgA, orgB string, refA, refB model.LiveRef) {
	t.Helper()
	orgA = newTestOrg(t, db, "Org A")
	orgB = newTestOrg(t, db, "Org B")
	now := time.Now()
	sA, err := db.BeginLiveSession(orgA, liveName, now)
	if err != nil {
		t.Fatalf("begin session org A: %v", err)
	}
	sB, err := db.BeginLiveSession(orgB, liveName, now)
	if err != nil {
		t.Fatalf("begin session org B: %v", err)
	}
	refA = model.LiveRef{ID: sA.ID, Name: sA.LiveName, OrgID: orgA}
	refB = model.LiveRef{ID: sB.ID, Name: sB.LiveName, OrgID: orgB}
	return orgA, orgB, refA, refB
}

func TestOrgSameLiveNameSeparateSessions(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	orgA := newTestOrg(t, db, "Org A")
	orgB := newTestOrg(t, db, "Org B")

	sA, err := db.BeginLiveSession(orgA, "live", now)
	if err != nil {
		t.Fatalf("begin A: %v", err)
	}
	sB, err := db.BeginLiveSession(orgB, "live", now)
	if err != nil {
		t.Fatalf("begin B: %v", err)
	}
	if sA.ID == sB.ID {
		t.Fatal("two organizations must never share a session")
	}
	if sA.OrgID != orgA || sB.OrgID != orgB {
		t.Fatalf("unexpected org ids: A=%q B=%q", sA.OrgID, sB.OrgID)
	}

	// Each organization resumes its own open session.
	resumedA, err := db.BeginLiveSession(orgA, "live", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("resume A: %v", err)
	}
	if resumedA.ID != sA.ID {
		t.Fatalf("expected org A to resume %s, got %s", sA.ID, resumedA.ID)
	}

	// Restarting in org A closes only org A's leftover session.
	if err := db.EndLiveSession(sA.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("end A: %v", err)
	}
	nextDay := now.Add(25 * time.Hour)
	if _, err := db.BeginLiveSession(orgA, "live", nextDay); err != nil {
		t.Fatalf("begin A next day: %v", err)
	}
	gotB, err := db.GetLiveSession(sB.ID)
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if gotB.EndedAt != "" {
		t.Fatal("a new session in org A must not close org B's session")
	}

	latestB, err := db.LatestLiveSession(orgB, "live")
	if err != nil {
		t.Fatalf("latest B: %v", err)
	}
	if latestB.ID != sB.ID {
		t.Fatalf("expected latest of org B to be %s, got %s", sB.ID, latestB.ID)
	}

	// An organization without sessions of that streamer finds nothing.
	orgC := newTestOrg(t, db, "Org C")
	if _, err := db.LatestLiveSession(orgC, "live"); !errors.Is(err, model.ErrLiveSessionNotFound) {
		t.Fatalf("expected ErrLiveSessionNotFound for org C, got %v", err)
	}
}

func TestOrgIsolationReads(t *testing.T) {
	db := openTestDB(t)
	orgA, orgB, refA, _ := twoOrgSessions(t, db, "live")

	if _, err := db.AddGift(refA, "user1", "User One", "Rose", 2, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}
	if err := db.LogAnomaly(refA, "spam", true, "SPAM", "user1"); err != nil {
		t.Fatalf("log anomaly: %v", err)
	}
	if err := db.AddUserMessageDedup(refA, "user1", "User One", "oi"); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if _, err := db.AddTargetGiftHistory(refA, "user1", "User One", "Rose", time.Now(), false); err != nil {
		t.Fatalf("add target gift: %v", err)
	}
	if _, err := db.AddPinnedComment(refA, "user1", "User One", "fixado", "pin-1", nil, time.Now()); err != nil {
		t.Fatalf("add pinned: %v", err)
	}
	if err := db.AddShare(refA, "user1", "User One"); err != nil {
		t.Fatalf("add share: %v", err)
	}
	if err := db.AddLike(refA, "user1", "User One", 4); err != nil {
		t.Fatalf("add like: %v", err)
	}
	if err := db.UpsertRoomLikeTotal(refA, 100); err != nil {
		t.Fatalf("upsert room like total: %v", err)
	}

	// counts returns, for one organization, how many rows each read sees.
	counts := func(orgID string) map[string]int {
		t.Helper()
		out := map[string]int{}
		must := func(name string, n int, err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("%s(%s): %v", name, orgID, err)
			}
			out[name] = n
		}
		gifts, err := db.GetRecentGifts(orgID, "live", 10)
		must("GetRecentGifts", len(gifts), err)
		byUser, err := db.GetGiftsByUser(orgID, "user1")
		must("GetGiftsByUser", len(byUser), err)
		summary, err := db.GetGiftSummary(orgID)
		must("GetGiftSummary", len(summary), err)
		mods, err := db.GetRecentModerations(orgID, 10)
		must("GetRecentModerations", len(mods), err)
		modsByLive, err := db.GetAnomalyLogsByLiveName(orgID, "live")
		must("GetAnomalyLogsByLiveName", len(modsByLive), err)
		modsByUser, err := db.GetAnomalyLogsByUser(orgID, "user1", 10)
		must("GetAnomalyLogsByUser", len(modsByUser), err)
		msgs, err := db.GetUserMessages(orgID, "user1")
		must("GetUserMessages", len(msgs), err)
		recent, err := db.GetUserMessagesRecent(orgID, "user1", 10)
		must("GetUserMessagesRecent", len(recent), err)
		all, err := db.GetAllUserMessages(orgID)
		must("GetAllUserMessages", len(all), err)
		distinct, err := db.TotalDistinctUsers(orgID)
		must("TotalDistinctUsers", distinct, err)
		history, err := db.GetRecentTargetGiftHistory(orgID, "live", 10)
		must("GetRecentTargetGiftHistory", len(history), err)
		pending, err := db.GetPendingTargetGiftHistory(orgID, "live", 10)
		must("GetPendingTargetGiftHistory", len(pending), err)
		pinned, err := db.GetRecentPinnedComments(orgID, "live", 10)
		must("GetRecentPinnedComments", len(pinned), err)
		shares, err := db.GetUserShareCount(orgID, "user1")
		must("GetUserShareCount", shares, err)
		likes, err := db.GetUserLikeTotal(orgID, "user1")
		must("GetUserLikeTotal", int(likes), err)
		room, delivered, err := db.LikeTotals(orgID, "live")
		must("LikeTotals.room", int(room), err)
		must("LikeTotals.delivered", int(delivered), err)
		stats, err := db.LiveStatsByUser(orgID, "live")
		must("LiveStatsByUser", len(stats), err)
		lives, err := db.RecentLivesForUser(orgID, "user1", 10)
		must("RecentLivesForUser", len(lives), err)
		first, err := db.LiveFirstSeen(orgID, "live")
		n := 0
		if first != "" {
			n = 1
		}
		must("LiveFirstSeen", n, err)
		return out
	}

	for name, n := range counts(orgA) {
		if n == 0 {
			t.Errorf("org A: %s must see its own data, got 0", name)
		}
	}
	for name, n := range counts(orgB) {
		if n != 0 {
			t.Errorf("org B: %s must not see org A's data, got %d", name, n)
		}
	}
}

func TestOrgIsolationClearAndDelete(t *testing.T) {
	db := openTestDB(t)
	orgA, orgB, refA, refB := twoOrgSessions(t, db, "live")

	for i := 0; i < 3; i++ {
		if _, err := db.AddGift(refA, "user1", "User", "Rose", 1, 0); err != nil {
			t.Fatalf("add gift A: %v", err)
		}
		if err := db.LogAnomaly(refA, "spam", true, "SPAM", "user1"); err != nil {
			t.Fatalf("log anomaly A: %v", err)
		}
	}
	if _, err := db.AddGift(refB, "user2", "User", "Rose", 1, 0); err != nil {
		t.Fatalf("add gift B: %v", err)
	}
	if err := db.LogAnomaly(refB, "spam", true, "SPAM", "user2"); err != nil {
		t.Fatalf("log anomaly B: %v", err)
	}

	modsA, err := db.GetRecentModerations(orgA, 10)
	if err != nil || len(modsA) != 3 {
		t.Fatalf("expected 3 moderations in org A, got %d (%v)", len(modsA), err)
	}

	// Deleting org A's moderation through org B is a no-op.
	deleted, err := db.DeleteModeration(orgB, modsA[0].ID)
	if err != nil {
		t.Fatalf("delete moderation cross-org: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("org B must not delete org A's moderation, deleted %d", deleted)
	}

	if deleted, err := db.ClearGifts(orgB); err != nil || deleted != 1 {
		t.Fatalf("ClearGifts(B): expected 1 deleted, got %d (%v)", deleted, err)
	}
	if deleted, err := db.ClearHistory(orgB); err != nil || deleted != 1 {
		t.Fatalf("ClearHistory(B): expected 1 deleted, got %d (%v)", deleted, err)
	}

	giftsA, err := db.GetRecentGifts(orgA, "live", 10)
	if err != nil || len(giftsA) != 3 {
		t.Fatalf("org A gifts must survive org B's clear, got %d (%v)", len(giftsA), err)
	}
	modsA, err = db.GetRecentModerations(orgA, 10)
	if err != nil || len(modsA) != 3 {
		t.Fatalf("org A moderations must survive org B's clear, got %d (%v)", len(modsA), err)
	}

	// Own-org delete still works.
	if deleted, err := db.DeleteModeration(orgA, modsA[0].ID); err != nil || deleted != 1 {
		t.Fatalf("DeleteModeration(A): expected 1, got %d (%v)", deleted, err)
	}
}

func TestOrgIsolationTargetGiftMutations(t *testing.T) {
	db := openTestDB(t)
	orgA, orgB, refA, _ := twoOrgSessions(t, db, "live")
	now := time.Now()

	id, err := db.AddTargetGiftHistory(refA, "user1", "User One", "Rose", now, false)
	if err != nil {
		t.Fatalf("add target gift: %v", err)
	}

	if err := db.MarkTargetGiftAnswered(orgB, id, model.TargetGiftResponseManual, now); !errors.Is(err, model.ErrInvalidID) {
		t.Fatalf("MarkTargetGiftAnswered cross-org: expected ErrInvalidID, got %v", err)
	}
	if err := db.SetTargetGiftPriority(orgB, id, true, now); !errors.Is(err, model.ErrInvalidID) {
		t.Fatalf("SetTargetGiftPriority(true) cross-org: expected ErrInvalidID, got %v", err)
	}
	if err := db.SetTargetGiftPriority(orgB, id, false, now); !errors.Is(err, model.ErrInvalidID) {
		t.Fatalf("SetTargetGiftPriority(false) cross-org: expected ErrInvalidID, got %v", err)
	}

	items, err := db.GetPendingTargetGiftHistory(orgA, "live", 10)
	if err != nil {
		t.Fatalf("get pending A: %v", err)
	}
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("expected the entry to stay pending in org A, got %+v", items)
	}
	if items[0].AnsweredAt != nil || items[0].IsPriority || items[0].PriorityAt != nil {
		t.Fatalf("cross-org calls must not change the row, got %+v", items[0])
	}

	// The owning organization can still mutate it.
	if err := db.SetTargetGiftPriority(orgA, id, true, now); err != nil {
		t.Fatalf("promote in org A: %v", err)
	}
	if err := db.MarkTargetGiftAnswered(orgA, id, model.TargetGiftResponseManual, now); err != nil {
		t.Fatalf("answer in org A: %v", err)
	}
}

func TestOrgIsolationListLives(t *testing.T) {
	db := openTestDB(t)
	orgA := newTestOrg(t, db, "Org A")
	orgB := newTestOrg(t, db, "Org B")

	seedOrgSession(t, db, orgA, "a1", "live", "2026-08-20")
	seedOrgSession(t, db, orgA, "a2", "other", "2026-08-21")
	seedOrgSession(t, db, orgB, "b1", "live", "2026-08-22")

	livesA, err := db.ListLives(orgA, 10)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(livesA) != 2 {
		t.Fatalf("expected 2 lives in org A, got %#v", livesA)
	}
	for _, l := range livesA {
		if l.ID == "b1" {
			t.Fatalf("org A must not list org B's session: %#v", livesA)
		}
	}

	livesB, err := db.ListLives(orgB, 10)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(livesB) != 1 || livesB[0].ID != "b1" {
		t.Fatalf("expected only b1 in org B, got %#v", livesB)
	}

	orgC := newTestOrg(t, db, "Org C")
	livesC, err := db.ListLives(orgC, 10)
	if err != nil {
		t.Fatalf("list C: %v", err)
	}
	if len(livesC) != 0 {
		t.Fatalf("expected no lives in org C, got %#v", livesC)
	}
}

func TestOrganizationCRUD(t *testing.T) {
	db := openTestDB(t)

	def, err := db.GetOrganization(model.DefaultOrgID)
	if err != nil {
		t.Fatalf("default organization must exist after migration: %v", err)
	}
	if !def.Active {
		t.Fatal("default organization must be active")
	}

	o, err := db.CreateOrganization("  Acme  ", 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if o.ID == "" || o.Name != "Acme" || o.MaxLives != model.DefaultOrgMaxLives || !o.Active || o.Members != 0 {
		t.Fatalf("unexpected organization: %+v", o)
	}

	got, err := db.GetOrganization(o.ID)
	if err != nil || got.ID != o.ID {
		t.Fatalf("get: %+v (%v)", got, err)
	}
	if _, err := db.GetOrganization("does-not-exist"); !errors.Is(err, model.ErrOrgNotFound) {
		t.Fatalf("expected ErrOrgNotFound, got %v", err)
	}
	if _, err := db.GetOrganization(" "); !errors.Is(err, model.ErrOrgNotFound) {
		t.Fatalf("expected ErrOrgNotFound for empty id, got %v", err)
	}

	list, err := db.ListOrganizations()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	seen := map[string]bool{}
	for _, org := range list {
		seen[org.ID] = true
	}
	if !seen[o.ID] || !seen[model.DefaultOrgID] {
		t.Fatalf("expected default and new organization in list, got %+v", list)
	}

	name, lives, inactive := "Acme 2", 7, false
	updated, err := db.UpdateOrganization(o.ID, &name, &lives, &inactive)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Acme 2" || updated.MaxLives != 7 || updated.Active {
		t.Fatalf("unexpected updated organization: %+v", updated)
	}

	// nil fields keep the current values.
	same, err := db.UpdateOrganization(o.ID, nil, nil, nil)
	if err != nil {
		t.Fatalf("noop update: %v", err)
	}
	if same.Name != "Acme 2" || same.MaxLives != 7 || same.Active {
		t.Fatalf("noop update changed fields: %+v", same)
	}

	if _, err := db.UpdateOrganization("does-not-exist", &name, nil, nil); !errors.Is(err, model.ErrOrgNotFound) {
		t.Fatalf("expected ErrOrgNotFound updating unknown org, got %v", err)
	}

	// The default organization can never be deactivated.
	if _, err := db.UpdateOrganization(model.DefaultOrgID, nil, nil, &inactive); err == nil {
		t.Fatal("expected error deactivating the default organization")
	}
	def, err = db.GetOrganization(model.DefaultOrgID)
	if err != nil || !def.Active {
		t.Fatalf("default organization must stay active: %+v (%v)", def, err)
	}
}

func TestOrganizationValidation(t *testing.T) {
	db := openTestDB(t)

	long := strings.Repeat("a", 81)
	bad := []struct {
		name     string
		orgName  string
		maxLives int
	}{
		{"empty name", "   ", 3},
		{"name too long", long, 3},
		{"negative lives", "Org", -1},
		{"too many lives", "Org", 51},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.CreateOrganization(tc.orgName, tc.maxLives); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	if _, err := db.CreateOrganization(strings.Repeat("é", 80), 50); err != nil {
		t.Fatalf("80 runes and 50 lives must be accepted: %v", err)
	}

	o, err := db.CreateOrganization("Org", 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	empty, zero := " ", 0
	if _, err := db.UpdateOrganization(o.ID, &empty, nil, nil); err == nil {
		t.Fatal("expected error for empty name on update")
	}
	if _, err := db.UpdateOrganization(o.ID, &long, nil, nil); err == nil {
		t.Fatal("expected error for long name on update")
	}
	if _, err := db.UpdateOrganization(o.ID, nil, &zero, nil); err == nil {
		t.Fatal("expected error for zero lives on update")
	}
	got, err := db.GetOrganization(o.ID)
	if err != nil || got.Name != "Org" || got.MaxLives != 1 {
		t.Fatalf("rejected updates must not change the organization: %+v (%v)", got, err)
	}
}

func TestOrganizationMembership(t *testing.T) {
	db := openTestDB(t)
	orgA := newTestOrg(t, db, "Org A")
	orgB := newTestOrg(t, db, "Org B")

	if _, err := db.GetMembership("user-1"); !errors.Is(err, model.ErrOrgNotFound) {
		t.Fatalf("expected ErrOrgNotFound for a user without organization, got %v", err)
	}

	m, err := db.UpsertOrgMember(orgA, "user-1", " a@example.com ", model.OrgRoleOwner)
	if err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	if m.OrgID != orgA || m.UserID != "user-1" || m.Email != "a@example.com" || m.Role != model.OrgRoleOwner {
		t.Fatalf("unexpected member: %+v", m)
	}
	if _, err := db.UpsertOrgMember(orgA, "user-2", "b@example.com", model.OrgRoleOperator); err != nil {
		t.Fatalf("upsert user-2: %v", err)
	}

	membersA, err := db.ListOrgMembers(orgA)
	if err != nil || len(membersA) != 2 {
		t.Fatalf("expected 2 members in org A, got %+v (%v)", membersA, err)
	}
	a, err := db.GetOrganization(orgA)
	if err != nil || a.Members != 2 {
		t.Fatalf("expected member count 2, got %+v (%v)", a, err)
	}

	// A user belongs to exactly one organization: upserting into B moves it.
	moved, err := db.UpsertOrgMember(orgB, "user-1", "a@example.com", model.OrgRoleOperator)
	if err != nil {
		t.Fatalf("move to B: %v", err)
	}
	if moved.OrgID != orgB || moved.Role != model.OrgRoleOperator {
		t.Fatalf("unexpected moved member: %+v", moved)
	}
	got, err := db.GetMembership("user-1")
	if err != nil || got.OrgID != orgB {
		t.Fatalf("expected membership in org B, got %+v (%v)", got, err)
	}
	membersA, err = db.ListOrgMembers(orgA)
	if err != nil || len(membersA) != 1 || membersA[0].UserID != "user-2" {
		t.Fatalf("expected only user-2 left in org A, got %+v (%v)", membersA, err)
	}

	// Deleting through the wrong organization is a no-op.
	ok, err := db.DeleteOrgMember(orgA, "user-1")
	if err != nil || ok {
		t.Fatalf("DeleteOrgMember wrong org: expected false, got %v (%v)", ok, err)
	}
	if got, err := db.GetMembership("user-1"); err != nil || got.OrgID != orgB {
		t.Fatalf("wrong-org delete must keep the membership, got %+v (%v)", got, err)
	}
	ok, err = db.DeleteOrgMember(orgB, "user-1")
	if err != nil || !ok {
		t.Fatalf("DeleteOrgMember: expected true, got %v (%v)", ok, err)
	}
	if _, err := db.GetMembership("user-1"); !errors.Is(err, model.ErrOrgNotFound) {
		t.Fatalf("expected ErrOrgNotFound after delete, got %v", err)
	}

	// Validation.
	if _, err := db.UpsertOrgMember(orgA, "user-3", "", "admin"); err == nil {
		t.Fatal("expected error for invalid role")
	}
	if _, err := db.UpsertOrgMember(orgA, " ", "", model.OrgRoleOwner); err == nil {
		t.Fatal("expected error for empty user id")
	}
	if _, err := db.UpsertOrgMember("does-not-exist", "user-3", "", model.OrgRoleOwner); !errors.Is(err, model.ErrOrgNotFound) {
		t.Fatalf("expected ErrOrgNotFound for unknown org, got %v", err)
	}
	if _, err := db.UpsertOrgMember("", "user-3", "", model.OrgRoleOwner); !errors.Is(err, model.ErrOrgRequired) {
		t.Fatalf("expected ErrOrgRequired, got %v", err)
	}
	if _, err := db.ListOrgMembers(" "); !errors.Is(err, model.ErrOrgRequired) {
		t.Fatalf("expected ErrOrgRequired listing members, got %v", err)
	}
}

func TestOrgRequired(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	calls := map[string]func(org string) error{
		"BeginLiveSession":     func(o string) error { _, err := db.BeginLiveSession(o, "live", now); return err },
		"LatestLiveSession":    func(o string) error { _, err := db.LatestLiveSession(o, "live"); return err },
		"GetRecentModerations": func(o string) error { _, err := db.GetRecentModerations(o, 10); return err },
		"GetAnomalyLogsByUser": func(o string) error { _, err := db.GetAnomalyLogsByUser(o, "u", 10); return err },
		"ClearHistory":         func(o string) error { _, err := db.ClearHistory(o); return err },
		"DeleteModeration":     func(o string) error { _, err := db.DeleteModeration(o, 1); return err },
		"GetUserMessages":      func(o string) error { _, err := db.GetUserMessages(o, "u"); return err },
		"GetUserMessagesRecent": func(o string) error {
			_, err := db.GetUserMessagesRecent(o, "u", 10)
			return err
		},
		"GetAllUserMessages": func(o string) error { _, err := db.GetAllUserMessages(o); return err },
		"GetRecentGifts":     func(o string) error { _, err := db.GetRecentGifts(o, "live", 10); return err },
		"GetGiftsByUser":     func(o string) error { _, err := db.GetGiftsByUser(o, "u"); return err },
		"GetGiftSummary":     func(o string) error { _, err := db.GetGiftSummary(o); return err },
		"ClearGifts":         func(o string) error { _, err := db.ClearGifts(o); return err },
		"MarkTargetGiftAnswered": func(o string) error {
			return db.MarkTargetGiftAnswered(o, 1, model.TargetGiftResponseManual, now)
		},
		"SetTargetGiftPriority": func(o string) error { return db.SetTargetGiftPriority(o, 1, true, now) },
		"GetRecentTargetGiftHistory": func(o string) error {
			_, err := db.GetRecentTargetGiftHistory(o, "live", 10)
			return err
		},
		"GetPendingTargetGiftHistory": func(o string) error {
			_, err := db.GetPendingTargetGiftHistory(o, "live", 10)
			return err
		},
		"GetRecentPinnedComments": func(o string) error {
			_, err := db.GetRecentPinnedComments(o, "live", 10)
			return err
		},
		"GetUserShareCount":  func(o string) error { _, err := db.GetUserShareCount(o, "u"); return err },
		"GetUserLikeTotal":   func(o string) error { _, err := db.GetUserLikeTotal(o, "u"); return err },
		"LikeTotals":         func(o string) error { _, _, err := db.LikeTotals(o, "live"); return err },
		"LiveFirstSeen":      func(o string) error { _, err := db.LiveFirstSeen(o, "live"); return err },
		"LiveStatsByUser":    func(o string) error { _, err := db.LiveStatsByUser(o, "live"); return err },
		"RecentLivesForUser": func(o string) error { _, err := db.RecentLivesForUser(o, "u", 10); return err },
		"TotalDistinctUsers": func(o string) error { _, err := db.TotalDistinctUsers(o); return err },
		"ListLives":          func(o string) error { _, err := db.ListLives(o, 10); return err },
	}
	for name, call := range calls {
		for _, org := range []string{"", "   "} {
			if err := call(org); !errors.Is(err, model.ErrOrgRequired) {
				t.Errorf("%s(%q): expected ErrOrgRequired, got %v", name, org, err)
			}
		}
	}
}
