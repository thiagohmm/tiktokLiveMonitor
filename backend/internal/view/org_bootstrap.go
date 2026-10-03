package view

import (
	"context"
	"github.com/thiagohmm/tiktok-live-monitor/internal/teams"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// bootstrapMemberships is intentionally a no-op: identities and memberships are
// imported or assigned explicitly. Restarting must never restore revoked access.
func (s *HTTPServer) bootstrapMemberships() {}

// ensureOwnOrganization gives a non-admin account without membership its own
// organization. Accounts that already have one only get their e-mail filled.
func (s *HTTPServer) ensureOwnOrganization(u auth.SubscriberProfile) (bool, error) {
	if u.Role == "admin" {
		return false, nil
	}
	repo := s.controller.Repository()
	if local, ok := s.admin.(*auth.Store); ok {
		revoked, err := local.WasOrganizationRevoked(u.ID)
		if err != nil {
			return false, err
		}
		if revoked {
			return false, nil
		}
	}
	if m, err := repo.GetMembership(u.ID); err == nil {
		if m.Email == "" && u.Email != "" {
			_, err = repo.UpsertOrgMember(m.OrgID, m.UserID, u.Email, m.Role)
		}
		return false, err
	}
	org, err := repo.CreateOrganization(accountOrgName(u), 0)
	if err != nil {
		return false, err
	}
	if _, err := repo.UpsertOrgMember(org.ID, u.ID, u.Email, model.OrgRoleOwner); err != nil {
		return false, err
	}
	if s.teams != nil {
		if err := s.teams.Allocate(context.Background(), org.ID, u.ID, teams.Allocation{PrimaryOwner: u.ID}, true); err != nil {
			return false, err
		}
	}
	return true, nil
}

// accountOrgName names the organization created for an existing account.
func accountOrgName(u auth.SubscriberProfile) string {
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		name = strings.TrimSpace(u.Email)
	}
	if name == "" {
		name = "Organização " + u.ID
	}
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	return name
}
