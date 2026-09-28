package view

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func TestAdminLivesAssignMovesLegacyLivesOnly(t *testing.T) {
	f := setupTenantFixture(t)
	legacy, err := f.repo.BeginLiveSession(model.DefaultOrgID, "legacylive", time.Now())
	if err != nil {
		t.Fatalf("begin legacy session: %v", err)
	}
	legacyRef := model.LiveRef{ID: legacy.ID, Name: legacy.LiveName, OrgID: model.DefaultOrgID}
	if _, err := f.repo.AddGift(legacyRef, "fan", "Fan", "Rosa", 2, 0); err != nil {
		t.Fatalf("seed gift: %v", err)
	}
	const target = "/api/admin/lives/assign"
	moveToA := `{"orgId":"` + f.orgA + `","liveNames":["legacylive"]}`

	for _, who := range []string{f.ownerA, f.operatorA, f.ownerB} {
		if rec := f.do(t, f.srv.handleAdminLivesAssign, who, http.MethodPost, target, moveToA); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: expected 403, got %d body=%s", who, rec.Code, rec.Body.String())
		}
		if rec := f.do(t, f.srv.handleAdminLivesAssign, who, http.MethodGet, target, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s GET: expected 403, got %d", who, rec.Code)
		}
	}
	if s, _ := f.repo.GetLiveSession(legacy.ID); s.OrgID != model.DefaultOrgID {
		t.Fatalf("a refused request must not move the session, got %q", s.OrgID)
	}

	rec := f.do(t, f.srv.handleAdminLivesAssign, f.admin, http.MethodGet, target, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var listing struct {
		OrgID string       `json:"orgId"`
		Lives []model.Live `json:"lives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	listed := map[string]bool{}
	for _, l := range listing.Lives {
		listed[l.ID] = true
	}
	if listing.OrgID != model.DefaultOrgID || !listed[legacy.ID] || listed[f.sessionA.ID] || listed[f.sessionB.ID] {
		t.Fatalf("admin must list only the legacy sessions, got %+v", listing)
	}

	inactive := false
	if _, err := f.repo.UpdateOrganization(f.orgB, nil, nil, &inactive); err != nil {
		t.Fatalf("disable org B: %v", err)
	}
	toB := `{"orgId":"` + f.orgB + `","sessionIds":["` + legacy.ID + `"]}`
	if rec := f.do(t, f.srv.handleAdminLivesAssign, f.admin, http.MethodPost, target, toB); rec.Code != http.StatusConflict {
		t.Fatalf("inactive destination: expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = f.do(t, f.srv.handleAdminLivesAssign, f.admin, http.MethodPost, target, moveToA)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin move: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var result model.LiveAssignResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Moved != 1 {
		t.Fatalf("expected 1 session moved, got %+v err=%v", result, err)
	}

	listLives := func(who string) []model.Live {
		t.Helper()
		rec := f.do(t, f.srv.handleAdminLives, who, http.MethodGet, "/api/admin/lives", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s lives: expected 200, got %d body=%s", who, rec.Code, rec.Body.String())
		}
		var payload struct {
			Lives []model.Live `json:"lives"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode lives: %v", err)
		}
		return payload.Lives
	}
	found := false
	for _, l := range listLives(f.ownerA) {
		found = found || l.ID == legacy.ID
	}
	if !found {
		t.Fatal("org A owner must see the moved live")
	}
	if gifts, _ := f.repo.GetRecentGifts(f.orgA, "legacylive", 10); len(gifts) != 1 {
		t.Fatalf("the gifts must follow the session, org A sees %d", len(gifts))
	}

	active := true
	if _, err := f.repo.UpdateOrganization(f.orgB, nil, nil, &active); err != nil {
		t.Fatalf("enable org B: %v", err)
	}
	for _, l := range listLives(f.ownerB) {
		if l.ID == legacy.ID {
			t.Fatal("org B must not see the live moved to org A")
		}
	}
	// A customer session never changes organization, not even by the admin.
	fromA := `{"orgId":"` + f.orgB + `","sessionIds":["` + legacy.ID + `"]}`
	if rec := f.do(t, f.srv.handleAdminLivesAssign, f.admin, http.MethodPost, target, fromA); rec.Code != http.StatusConflict {
		t.Fatalf("moving a customer session: expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if s, _ := f.repo.GetLiveSession(legacy.ID); s.OrgID != f.orgA {
		t.Fatalf("session must stay in org A, got %q", s.OrgID)
	}
}
