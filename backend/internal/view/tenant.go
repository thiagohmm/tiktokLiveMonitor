package view

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/tenant"
)

// localDevUser is the synthetic user used only when auth is disabled
// (docker-compose local development). It belongs to the default organization.
const localDevUser = "local-dev"

const (
	msgNoOrganization  = "Sua conta não está vinculada a nenhuma organização. Peça ao administrador para adicioná-la."
	msgOrgDisabled     = "A organização desta conta está desativada."
	msgOrgOwnerOnly    = "Apenas o dono da organização pode fazer esta operação."
	errCodeNoOrg       = "no_organization"
	errCodeOrgDisabled = "organization_disabled"
)

// tenantResolver always reads current membership and organization state.
type tenantResolver struct{ repo model.OrganizationRepository }

func newTenantResolver(repo model.OrganizationRepository) *tenantResolver {
	return &tenantResolver{repo: repo}
}

// resolve returns the tenant of user, or an error code (no_organization /
// organization_disabled) when the user cannot act inside any organization.
func (tr *tenantResolver) resolve(user *auth.User) (tenant.Tenant, string, error) {

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

	return t, code, nil
}

// invalidate is retained for callers; authorization is no longer cached.
func (tr *tenantResolver) invalidate(userID string) {}

// tenantExemptAdmin lists the platform-administration paths that skip tenant
// resolution. They are exempt from organization resolution but NOT from the
// platform-admin check enforced in tenantMiddleware — antes, a checagem de
// papel ficava inteiramente a cargo do RequireAdmin de cada handler.
func tenantExemptAdmin(path string) bool {
	return strings.HasPrefix(path, "/api/admin/users") ||
		strings.HasPrefix(path, "/api/admin/orgs") ||
		path == "/api/admin/lives/assign"
}

// tenantExempt lists authenticated paths that work without an organization:
// the session endpoints (the UI must be able to explain the missing
// membership) and the platform administration.
func tenantExempt(path string) bool {
	return strings.HasPrefix(path, "/api/auth/") || tenantExemptAdmin(path)
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
		// Defesa em profundidade: as rotas de administração da plataforma sao
		// isentas de resolucao de organizacao, entao o papel admin passa a ser
		// verificado aqui tambem — nao apenas no RequireAdmin interno de cada
		// handler. Uma rota nova sob esses prefixos sem RequireAdmin deixa de
		// ficar acessivel a qualquer usuario autenticado.
		if tenantExemptAdmin(r.URL.Path) && user.Role != "admin" {
			writeError(w, http.StatusForbidden, "apenas o administrador da plataforma")
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

		if !tenantExempt(r.URL.Path) && !t.PlatformAdmin && s.teams != nil {
			if err := s.teams.Access(r.Context(), t.OrgID, t.UserID); err != nil {
				writeJSONErrorCode(w, 403, "seat_suspended", err.Error())
				return
			}
		}
		if !t.CanManageOrg() && !operatorReadPath(r.Method, r.URL.Path) {
			writeError(w, 403, "membros ajudantes possuem acesso somente de leitura")
			return
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

// operatorReadPath grants only explicit observational endpoints and identity operations.
func operatorReadPath(method, path string) bool {
	if strings.HasPrefix(path, "/api/auth/") {
		return true
	}
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	if strings.HasPrefix(path, "/api/pix/media/") || strings.HasPrefix(path, "/api/pix/contacts/") {
		return true
	}
	if strings.HasPrefix(path, "/api/pix/tickets/") {
		return true
	}
	switch path {
	case "/events", "/api/state", "/api/lives", "/api/settings", "/api/history", "/api/gifts", "/api/available-gifts", "/api/target-gift-history", "/api/pinned-comments", "/api/ranking", "/api/report", "/api/profile", "/api/goals", "/api/pix/tickets", "/api/pix/values", "/api/pix/whatsapp/status", "/api/org/allowed-lives":
		return true
	}
	return false
}
func writeJSONErrorCode(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "error": message})
}
