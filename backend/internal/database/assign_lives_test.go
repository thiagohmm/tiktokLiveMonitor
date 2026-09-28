package database

import (
	"errors"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// seedLegacySession opens a legacy-organization session of liveName on day
// and writes one row in every event table.
func seedLegacySession(t *testing.T, db *DB, liveName string, day time.Time) model.LiveRef {
	t.Helper()
	s, err := db.BeginLiveSession(model.DefaultOrgID, liveName, day)
	if err != nil {
		t.Fatalf("begin legacy session %s: %v", liveName, err)
	}
	ref := model.LiveRef{ID: s.ID, Name: s.LiveName, OrgID: model.DefaultOrgID}
	if _, err := db.AddGift(ref, "fan", "Fan", "Rose", 3, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}
	if err := db.AddUserMessageDedup(ref, "fan", "Fan", "oi "+s.ID); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if err := db.AddLike(ref, "fan", "Fan", 5); err != nil {
		t.Fatalf("add like: %v", err)
	}
	if err := db.UpsertRoomLikeTotal(ref, 50); err != nil {
		t.Fatalf("upsert room like total: %v", err)
	}
	if err := db.AddShare(ref, "fan", "Fan"); err != nil {
		t.Fatalf("add share: %v", err)
	}
	if err := db.LogAnomaly(ref, "spam", true, "SPAM", "fan"); err != nil {
		t.Fatalf("log anomaly: %v", err)
	}
	if _, err := db.AddTargetGiftHistory(ref, "fan", "Fan", "Rose", day, false); err != nil {
		t.Fatalf("add target gift: %v", err)
	}
	if _, err := db.AddPinnedComment(ref, "fan", "Fan", "fixado", "pin-"+s.ID, nil, day); err != nil {
		t.Fatalf("add pinned: %v", err)
	}
	if _, err := db.AddGiftGoal(model.GiftGoal{LiveID: ref.ID, LiveName: ref.Name, Title: "Meta", TargetUnits: 10}); err != nil {
		t.Fatalf("add goal: %v", err)
	}
	return ref
}

// liveView counts what one organization sees of a live through the
// tenant-scoped reads.
type liveView struct {
	lives, gifts, pinned, targets, anomalies, shares, messages int
	roomLikes, likes                                           int64
}

func viewOf(t *testing.T, db *DB, orgID, liveName string) liveView {
	t.Helper()
	var v liveView
	lives, err := db.ListLives(orgID, 100)
	if err != nil {
		t.Fatalf("list lives: %v", err)
	}
	for _, l := range lives {
		if l.Name == liveName {
			v.lives++
		}
	}
	gifts, err := db.GetRecentGifts(orgID, liveName, 100)
	if err != nil {
		t.Fatalf("gifts: %v", err)
	}
	v.gifts = len(gifts)
	pinned, err := db.GetRecentPinnedComments(orgID, liveName, 100)
	if err != nil {
		t.Fatalf("pinned: %v", err)
	}
	v.pinned = len(pinned)
	targets, err := db.GetRecentTargetGiftHistory(orgID, liveName, 100)
	if err != nil {
		t.Fatalf("target gifts: %v", err)
	}
	v.targets = len(targets)
	anomalies, err := db.GetAnomalyLogsByLiveName(orgID, liveName)
	if err != nil {
		t.Fatalf("anomalies: %v", err)
	}
	v.anomalies = len(anomalies)
	if v.roomLikes, v.likes, err = db.LikeTotals(orgID, liveName); err != nil {
		t.Fatalf("like totals: %v", err)
	}
	stats, err := db.LiveStatsByUser(orgID, liveName)
	if err != nil {
		t.Fatalf("live stats: %v", err)
	}
	for _, s := range stats {
		v.shares += s.ShareCount
		v.messages += s.MessageCount
	}
	return v
}

func TestAssignLegacyLivesByNameMovesSessionsAndEvents(t *testing.T) {
	db := openTestDB(t)
	day1 := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	day2 := day1.Add(48 * time.Hour)
	refs := []model.LiveRef{
		seedLegacySession(t, db, "streamer", day1),
		seedLegacySession(t, db, "streamer", day2),
	}
	seedLegacySession(t, db, "other", day1)
	orgA := newTestOrg(t, db, "Org A")
	orgB := newTestOrg(t, db, "Org B")
	if err := db.SetSetting(OrgSettingsKey(model.DefaultOrgID), `{"targetGifts":["Rose"]}`); err != nil {
		t.Fatalf("seed legacy settings: %v", err)
	}

	before := viewOf(t, db, model.DefaultOrgID, "streamer")
	if before.lives != 2 || before.gifts != 2 {
		t.Fatalf("legacy should see the seeded sessions first, got %+v", before)
	}

	res, err := db.AssignLegacyLives(model.LiveAssignment{OrgID: orgA, LiveNames: []string{"streamer"}, CopySettings: true})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if res.Moved != 2 || len(res.LiveNames) != 1 || res.LiveNames[0] != "streamer" || !res.SettingsCopied {
		t.Fatalf("unexpected result %+v", res)
	}

	if got := viewOf(t, db, orgA, "streamer"); got != before {
		t.Fatalf("org A must see every event of the moved sessions: want %+v, got %+v", before, got)
	}
	if got := viewOf(t, db, model.DefaultOrgID, "streamer"); got != (liveView{}) {
		t.Fatalf("legacy must no longer see the moved live, got %+v", got)
	}
	if got := viewOf(t, db, orgB, "streamer"); got != (liveView{}) {
		t.Fatalf("org B must see nothing, got %+v", got)
	}
	if got := viewOf(t, db, model.DefaultOrgID, "other"); got.lives != 1 || got.gifts != 1 {
		t.Fatalf("other legacy lives must stay in the legacy organization, got %+v", got)
	}
	for _, ref := range refs {
		s, err := db.GetLiveSession(ref.ID)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if s.OrgID != orgA || s.EndedAt == "" {
			t.Fatalf("moved session must belong to org A and be closed, got %+v", s)
		}
		goals, err := db.GetGiftGoals(model.LiveRef{ID: ref.ID, Name: ref.Name, OrgID: orgA})
		if err != nil || len(goals) != 1 {
			t.Fatalf("goal must follow its session, got %d err=%v", len(goals), err)
		}
	}
	// A new session of the same streamer in org A starts fresh (the moved,
	// closed sessions are history, not the current live).
	next, err := db.BeginLiveSession(orgA, "streamer", day2.Add(time.Hour))
	if err != nil {
		t.Fatalf("begin in org A: %v", err)
	}
	if next.ID == refs[1].ID {
		t.Fatal("org A must not resume a moved legacy session")
	}

	settings, err := db.GetSetting(OrgSettingsKey(orgA))
	if err != nil || settings != `{"targetGifts":["Rose"]}` {
		t.Fatalf("legacy settings must be copied to org A, got %q err=%v", settings, err)
	}
	if s, _ := db.GetSetting(OrgSettingsKey(orgB)); s != "" {
		t.Fatalf("org B settings must stay untouched, got %q", s)
	}
}

func TestAssignLegacyLivesBySessionIDMovesOnlyThatSession(t *testing.T) {
	db := openTestDB(t)
	day1 := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	first := seedLegacySession(t, db, "streamer", day1)
	second := seedLegacySession(t, db, "streamer", day1.Add(48*time.Hour))
	orgA := newTestOrg(t, db, "Org A")

	res, err := db.AssignLegacyLives(model.LiveAssignment{OrgID: orgA, SessionIDs: []string{first.ID, first.ID}})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if res.Moved != 1 || res.SettingsCopied {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := viewOf(t, db, orgA, "streamer"); got.lives != 1 || got.gifts != 1 {
		t.Fatalf("org A must see exactly the moved session, got %+v", got)
	}
	if s, _ := db.GetLiveSession(second.ID); s.OrgID != model.DefaultOrgID {
		t.Fatalf("the other session must stay in the legacy organization, got %q", s.OrgID)
	}
	if s, _ := db.GetSetting(OrgSettingsKey(orgA)); s != "" {
		t.Fatalf("settings are only copied on request, got %q", s)
	}
}

func TestAssignLegacyLivesRefusals(t *testing.T) {
	db := openTestDB(t)
	day := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	legacy := seedLegacySession(t, db, "streamer", day)
	orgA := newTestOrg(t, db, "Org A")
	orgB := newTestOrg(t, db, "Org B")
	sA, err := db.BeginLiveSession(orgA, "clientlive", day)
	if err != nil {
		t.Fatalf("begin org A session: %v", err)
	}
	inactive := newTestOrg(t, db, "Inactive")
	off := false
	if _, err := db.UpdateOrganization(inactive, nil, nil, &off); err != nil {
		t.Fatalf("disable org: %v", err)
	}

	cases := []struct {
		name string
		a    model.LiveAssignment
		want error
	}{
		{"inactive destination", model.LiveAssignment{OrgID: inactive, SessionIDs: []string{legacy.ID}}, model.ErrOrgInactive},
		{"unknown destination", model.LiveAssignment{OrgID: "missing-org", SessionIDs: []string{legacy.ID}}, model.ErrOrgNotFound},
		{"customer session to another customer", model.LiveAssignment{OrgID: orgB, SessionIDs: []string{sA.ID}}, model.ErrLiveNotLegacy},
		{"mixed list is all or nothing", model.LiveAssignment{OrgID: orgB, SessionIDs: []string{legacy.ID, sA.ID}}, model.ErrLiveNotLegacy},
		{"customer live name", model.LiveAssignment{OrgID: orgB, LiveNames: []string{"clientlive"}}, model.ErrLiveSessionNotFound},
		{"unknown session", model.LiveAssignment{OrgID: orgB, SessionIDs: []string{"nope"}}, model.ErrLiveSessionNotFound},
	}
	for _, c := range cases {
		if _, err := db.AssignLegacyLives(c.a); !errors.Is(err, c.want) {
			t.Fatalf("%s: expected %v, got %v", c.name, c.want, err)
		}
	}
	for name, a := range map[string]model.LiveAssignment{
		"legacy destination": {OrgID: model.DefaultOrgID, SessionIDs: []string{legacy.ID}},
		"nothing selected":   {OrgID: orgB},
		"no destination":     {SessionIDs: []string{legacy.ID}},
	} {
		if _, err := db.AssignLegacyLives(a); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}

	if s, _ := db.GetLiveSession(legacy.ID); s.OrgID != model.DefaultOrgID || s.EndedAt != "" {
		t.Fatalf("refused moves must leave the legacy session untouched, got %+v", s)
	}
	if s, _ := db.GetLiveSession(sA.ID); s.OrgID != orgA {
		t.Fatalf("org A session must never change organization, got %q", s.OrgID)
	}
	if got := viewOf(t, db, orgB, "clientlive"); got != (liveView{}) {
		t.Fatalf("org B must see nothing of org A, got %+v", got)
	}
}
