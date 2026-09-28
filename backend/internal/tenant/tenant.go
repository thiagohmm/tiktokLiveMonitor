// Package tenant carries the organization of the authenticated caller through
// the request context. Every tenant-scoped handler reads it from here; the
// organization never comes from the request body or query string.
package tenant

import (
	"context"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

type contextKey struct{}

// Tenant is the organization context of one request.
type Tenant struct {
	OrgID  string
	UserID string
	Email  string
	// Role is the role inside the organization (model.OrgRoleOwner/Operator).
	Role string
	// PlatformAdmin is the Supabase app_metadata admin (manages every organization).
	PlatformAdmin bool
}

// CanManageOrg reports whether the caller may manage the organization's members
// and destructive operations (owner of the organization or platform admin).
func (t Tenant) CanManageOrg() bool {
	return t.Role == model.OrgRoleOwner || t.PlatformAdmin
}

// With returns a context carrying t.
func With(ctx context.Context, t Tenant) context.Context {
	return context.WithValue(ctx, contextKey{}, t)
}

// From returns the tenant of the request, if resolved.
func From(ctx context.Context) (Tenant, bool) {
	t, ok := ctx.Value(contextKey{}).(Tenant)
	if !ok || t.OrgID == "" {
		return Tenant{}, false
	}
	return t, true
}
