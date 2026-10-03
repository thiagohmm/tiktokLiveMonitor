package view

import (
	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"testing"
)

func TestRestartDoesNotCreateOrganizationsForRevokedUsers(t *testing.T) {
	srv, repo, _, _ := setupTestServer(t)
	local := srv.auth.Store
	if _, err := local.ImportUsers(t.Context(), []auth.ImportAccount{{SubscriberProfile: auth.SubscriberProfile{ID: "revoked", Email: "revoked@example.com", Role: "subscriber", Active: true}}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := local.DB.Exec(`UPDATE users SET organization_revoked=true WHERE id='revoked'`); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.ListOrganizations()
	srv.bootstrapMemberships()
	profile, err := local.GetProfileByID("revoked")
	if err != nil {
		t.Fatal(err)
	}
	if created, err := srv.ensureOwnOrganization(*profile); err != nil || created {
		t.Fatalf("revoked identity recreated: %v", err)
	}
	after, _ := repo.ListOrganizations()
	if len(before) != len(after) {
		t.Fatal("organization recreated")
	}
	if _, err := repo.GetMembership("revoked"); err != model.ErrOrgNotFound {
		t.Fatal("membership recreated")
	}
}
