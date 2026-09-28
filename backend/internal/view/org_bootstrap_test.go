package view

import (
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// fakeDirectory is an in-memory account directory (Supabase stand-in).
type fakeDirectory struct {
	auth.AccountDirectory
	users []auth.SubscriberProfile
}

func (d *fakeDirectory) ListSubscribers() ([]auth.SubscriberProfile, error) { return d.users, nil }

func TestBootstrapGivesEachAccountItsOwnOrganization(t *testing.T) {
	srv, repo, _, _ := setupTestServer(t)
	srv.auth = auth.Config{Enabled: true, ServiceRoleKey: "service", SupabaseURL: "http://supabase.invalid", SupabaseAnon: "anon"}
	srv.admin = &fakeDirectory{users: []auth.SubscriberProfile{
		{ID: "cliente-a", Email: "a@example.com", Role: "subscriber", Active: true},
		{ID: "cliente-b", Email: "b@example.com", DisplayName: "Loja B", Role: "subscriber", Active: true},
		{ID: "root", Email: "root@example.com", Role: "admin", Active: true},
	}}

	srv.bootstrapMemberships()

	a, err := repo.GetMembership("cliente-a")
	if err != nil {
		t.Fatalf("membership A: %v", err)
	}
	b, err := repo.GetMembership("cliente-b")
	if err != nil {
		t.Fatalf("membership B: %v", err)
	}
	if a.OrgID == b.OrgID || a.OrgID == model.DefaultOrgID || b.OrgID == model.DefaultOrgID {
		t.Fatalf("accounts must get separate, non-legacy organizations: A=%s B=%s", a.OrgID, b.OrgID)
	}
	if a.Role != model.OrgRoleOwner || b.Role != model.OrgRoleOwner {
		t.Fatalf("accounts must own their organization: %+v %+v", a, b)
	}
	if org, err := repo.GetOrganization(b.OrgID); err != nil || org.Name != "Loja B" {
		t.Fatalf("organization of B = %+v, %v", org, err)
	}
	if _, err := repo.GetMembership("root"); err != model.ErrOrgNotFound {
		t.Fatalf("platform admin must not get a membership, got %v", err)
	}

	before, _ := repo.ListOrganizations()
	srv.bootstrapMemberships()
	after, _ := repo.ListOrganizations()
	if len(after) != len(before) {
		t.Fatalf("bootstrap is not idempotent: %d -> %d organizations", len(before), len(after))
	}
}
