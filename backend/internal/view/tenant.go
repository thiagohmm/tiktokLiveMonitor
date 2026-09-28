package view

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/tenant"
)

// localDevUser is the synthetic user used only when auth is disabled
// (docker-compose dev without Supabase). It belongs to the default organization.
const localDevUser = "local-dev"

// tenantCacheTTL bounds how long a membership (or its absence) is reused. An
// admin change invalidates the entry right away; the TTL only covers changes
// made directly in the database.
const tenantCacheTTL = 30 * time.Second

const (
	msgNoOrganization  = "Sua conta não está vinculada a nenhuma organização. Peça ao administrador para adicioná-la."
	msgOrgDisabled     = "A organização desta conta está desativada."
	msgOrgOwnerOnly    = "Apenas o dono da organização pode fazer esta operação."
	errCodeNoOrg       = "no_organization"
	errCodeOrgDisabled = "organization_disabled"
)

type tenantEntry struct {
	t       tenant.Tenant
	code    string
	expires time.Time
}

// tenantResolver maps authenticated users to their organization.
type tenantResolver struct {
	repo  model.OrganizationRepository
	mu    sync.Mutex
	cache map[string]tenantEntry
}

func newTenantResolver(repo model.OrganizationRepository) *tenantResolver {
	return &tenantResolver{repo: repo, cache: make(map[string]tenantEntry)}
}

// resolve returns the tenant of user, or an error code (no_organization /
// organization_disabled) when the user cannot act inside any organization.
func (tr *tenantResolver) resolve(user *auth.User) (tenant.Tenant, string, error) {
	now := time.Now()
	tr.mu.Lock()
	if e, ok := tr.cache[user.ID]; ok && now.Before(e.expires) {
		tr.mu.Unlock()
		return e.t, e.code, nil
	}
	tr.mu.Unlock()

	t := tenant.Tenant{UserID: user.ID, Email: user.Email, PlatformAdmin: user.Role == "admin"}
	code := ""
	member, err := tr.repo.GetMembership(user.ID)
	switch {
	case err == nil:
		t.OrgID, t.Role = member.OrgID, member.Role
	case errors.Is(err, model.ErrOrgNotFound):
		if t.PlatformAdmin {
			// Platform admins without a membership operate the default
			// organization (the pre-multi-tenant data lives there).
			t.OrgID, t.Role = model.DefaultOrgID, model.OrgRoleOwner
		} else {
			code = errCodeNoOrg
		}
	default:
		return tenant.Tenant{}, "", err
	}
	if t.OrgID != "" {
		org, err := tr.repo.GetOrganization(t.OrgID)
		switch {
		case errors.Is(err, model.ErrOrgNotFound):
			t, code = tenant.Tenant{UserID: user.ID, PlatformAdmin: t.PlatformAdmin}, errCodeNoOrg
		case err != nil:
			return tenant.Tenant{}, "", err
		case !org.Active:
			t, code = tenant.Tenant{UserID: user.ID, PlatformAdmin: t.PlatformAdmin}, errCodeOrgDisabled
		}
	}

	tr.mu.Lock()
	if len(tr.cache) > 10000 {
		tr.cache = make(map[string]tenantEntry)
	}
	tr.cache[user.ID] = tenantEntry{t: t, code: code, expires: now.Add(tenantCacheTTL)}
	tr.mu.Unlock()
	return t, code, nil
}

// invalidate drops cached memberships (all of them when userID is empty).
func (tr *tenantResolver) invalidate(userID string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if userID == "" {
		tr.cache = make(map[string]tenantEntry)
		return
	}
	delete(tr.cache, userID)
}

// tenantExempt lists authenticated paths that work without an organization:
// the session endpoints (the UI must be able to explain the missing
// membership) and the platform administration.
func tenantExempt(path string) bool {
	return strings.HasPrefix(path, "/api/auth/") ||
		strings.HasPrefix(path, "/api/admin/users") ||
		strings.HasPrefix(path, "/api/admin/orgs") ||
		path == "/api/admin/lives/assign"
}

// tenantMiddleware resolves the organization of every authenticated request
// and rejects tenant-scoped requests of users without an active organization.
// It runs after auth.Middleware.
func (s *HTTPServer) tenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.PublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.auth.Enabled {
			t := tenant.Tenant{OrgID: model.DefaultOrgID, UserID: localDevUser, Role: model.OrgRoleOwner, PlatformAdmin: true}
			// A dev database upgraded from the per-user Fila PIX gave
			// local-dev its own organization; keep using it.
			if m, err := s.controller.Repository().GetMembership(localDevUser); err == nil {
				t.OrgID = m.OrgID
			}
			next.ServeHTTP(w, r.WithContext(tenant.With(r.Context(), t)))
			return
		}
		user, ok := auth.UserFromContext(r.Context())
		if !ok || user == nil || strings.TrimSpace(user.ID) == "" {
			writeError(w, http.StatusUnauthorized, "não autorizado")
			return
		}
		t, code, err := s.tenants.resolve(user)
		if err != nil {
			log.Printf("[View] resolve tenant for %s: %v", user.ID, err)
			writeError(w, http.StatusServiceUnavailable, "não foi possível carregar a organização")
			return
		}
		if code != "" && !tenantExempt(r.URL.Path) {
			msg := msgNoOrganization
			if code == errCodeOrgDisabled {
				msg = msgOrgDisabled
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
			return
		}
		if t.OrgID != "" {
			r = r.WithContext(tenant.With(r.Context(), t))
		}
		next.ServeHTTP(w, r)
	})
}

// requestTenant returns the organization context of the request, answering
// 403 when the caller has none.
func requestTenant(w http.ResponseWriter, r *http.Request) (tenant.Tenant, bool) {
	t, ok := tenant.From(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, msgNoOrganization)
		return tenant.Tenant{}, false
	}
	return t, true
}

// requireOrgManager returns the tenant when the caller may manage its
// organization (owner or platform admin), answering 403 otherwise.
func requireOrgManager(w http.ResponseWriter, r *http.Request) (tenant.Tenant, bool) {
	t, ok := requestTenant(w, r)
	if !ok {
		return tenant.Tenant{}, false
	}
	if !t.CanManageOrg() {
		writeError(w, http.StatusForbidden, msgOrgOwnerOnly)
		return tenant.Tenant{}, false
	}
	return t, true
}
