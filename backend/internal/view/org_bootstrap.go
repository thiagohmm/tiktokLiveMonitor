package view

import (
	"log"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// orgBootstrapMarker is the settings key recording that the one-time account
// bootstrap already ran.
const orgBootstrapMarker = "migration:org_bootstrap_v1"

// bootstrapMemberships runs once after the multi-tenant upgrade: every
// Supabase account (except platform admins) without a membership gets its own
// organization with itself as owner. Accounts never share an organization
// by default, so no customer sees another customer's data; the platform admin
// can later merge people into a team. Accounts live in Supabase (profiles),
// so this cannot be done by the SQL migration of the operational database.
func (s *HTTPServer) bootstrapMemberships() {
	if s.admin == nil || !s.auth.Enabled || s.auth.ServiceRoleKey == "" {
		return
	}
	repo := s.controller.Repository()
	if done, err := repo.GetSetting(orgBootstrapMarker); err == nil && done != "" {
		return
	}
	users, err := s.admin.ListSubscribers()
	if err != nil {
		log.Printf("[View] organizations bootstrap: list accounts: %v", err)
		return
	}
	created, failed := 0, 0
	for _, u := range users {
		ok, err := s.ensureOwnOrganization(u)
		if err != nil {
			log.Printf("[View] organizations bootstrap: %s: %v", u.ID, err)
			failed++
			continue
		}
		if ok {
			created++
		}
	}
	s.tenants.invalidate("")
	if failed == 0 {
		if err := repo.SetSetting(orgBootstrapMarker, "done"); err != nil {
			log.Printf("[View] organizations bootstrap: marker: %v", err)
		}
	}
	log.Printf("[View] organizations bootstrap: %d organizações criadas (%d falhas)", created, failed)
}

// ensureOwnOrganization gives a non-admin account without membership its own
// organization. Accounts that already have one only get their e-mail filled.
func (s *HTTPServer) ensureOwnOrganization(u auth.SubscriberProfile) (bool, error) {
	if u.Role == "admin" {
		return false, nil
	}
	repo := s.controller.Repository()
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
