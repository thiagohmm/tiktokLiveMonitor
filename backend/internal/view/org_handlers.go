package view

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// writeOrgError maps organization repository errors to HTTP statuses.
// Validation messages of the repository are safe to show to the caller.
func writeOrgError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, model.ErrOrgNotFound):
		writeError(w, http.StatusNotFound, "organização não encontrada")
	case errors.Is(err, model.ErrOrgRequired):
		writeError(w, http.StatusBadRequest, "organização é obrigatória")
	case strings.Contains(err.Error(), "query ") || strings.Contains(err.Error(), "insert ") ||
		strings.Contains(err.Error(), "update ") || strings.Contains(err.Error(), "list ") ||
		strings.Contains(err.Error(), "upsert ") || strings.Contains(err.Error(), "delete "):
		writeInternalError(w, r, err)
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// createActiveAccount creates a Supabase user that can log in right away
// (organization members are released by whoever adds them).
func (s *HTTPServer) createActiveAccount(req auth.CreateSubscriberRequest) (*auth.SubscriberProfile, error) {
	user, err := s.admin.CreateSubscriber(req)
	if err != nil {
		return nil, err
	}
	active := true
	updated, err := s.admin.UpdateSubscriber(auth.UpdateSubscriberRequest{ID: user.ID, Active: &active})
	if err != nil {
		_ = s.admin.DeleteSubscriber(user.ID)
		return nil, err
	}
	return updated, nil
}

// addMember creates an active account and binds it to orgID. The account is
// removed again when the membership cannot be stored, so no user is left
// without an organization.
func (s *HTTPServer) addMember(orgID, role string, req auth.CreateSubscriberRequest) (model.OrgMember, error) {
	if !model.ValidOrgRole(role) {
		return model.OrgMember{}, errors.New("papel inválido (use owner ou operator)")
	}
	if _, err := s.controller.Repository().GetOrganization(orgID); err != nil {
		return model.OrgMember{}, err
	}
	user, err := s.createActiveAccount(req)
	if err != nil {
		return model.OrgMember{}, err
	}
	member, err := s.controller.Repository().UpsertOrgMember(orgID, user.ID, user.Email, role)
	if err != nil {
		_ = s.admin.DeleteSubscriber(user.ID)
		return model.OrgMember{}, err
	}
	s.tenants.invalidate(user.ID)
	return member, nil
}

// ---- Platform administration (role admin) ----

type orgWithMembers struct {
	model.Organization
	MemberList []model.OrgMember `json:"memberList"`
}

// handleAdminOrgs lists (GET) or creates (POST) organizations. Only platform
// admins reach it. POST may also create the owner account of the new org.
func (s *HTTPServer) handleAdminOrgs(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.RequireAdmin(w, r, s.auth); !ok {
		return
	}
	repo := s.controller.Repository()
	switch r.Method {
	case http.MethodGet:
		orgs, err := repo.ListOrganizations()
		if err != nil {
			writeInternalError(w, r, err)
			return
		}
		out := make([]orgWithMembers, 0, len(orgs))
		for _, o := range orgs {
			members, err := repo.ListOrgMembers(o.ID)
			if err != nil {
				writeInternalError(w, r, err)
				return
			}
			out = append(out, orgWithMembers{Organization: o, MemberList: members})
		}
		writeJSON(w, map[string]any{"organizations": out})
	case http.MethodPost:
		var body struct {
			Name          string `json:"name"`
			MaxLives      int    `json:"maxLives"`
			OwnerEmail    string `json:"ownerEmail"`
			OwnerPassword string `json:"ownerPassword"`
			OwnerName     string `json:"ownerName"`
			// OwnerUserID moves an existing account into the new organization
			// as its owner.
			OwnerUserID string `json:"ownerUserId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		withOwner := strings.TrimSpace(body.OwnerEmail) != ""
		existingOwner := strings.TrimSpace(body.OwnerUserID)
		if (withOwner || existingOwner != "") && s.admin == nil {
			writeError(w, http.StatusServiceUnavailable, "supabase admin não configurado")
			return
		}
		org, err := repo.CreateOrganization(body.Name, body.MaxLives)
		if err != nil {
			writeOrgError(w, r, err)
			return
		}
		resp := map[string]any{"organization": org}
		if existingOwner != "" {
			profile, err := s.admin.GetProfileByID(existingOwner)
			if err != nil {
				resp["ownerError"] = "usuário não encontrado"
			} else {
				previous, prevErr := repo.GetMembership(existingOwner)
				member, err := repo.UpsertOrgMember(org.ID, existingOwner, profile.Email, model.OrgRoleOwner)
				if err != nil {
					resp["ownerError"] = err.Error()
				} else {
					if prevErr == nil && previous.OrgID != org.ID {
						s.controller.DetachUser(previous.OrgID, existingOwner)
					}
					s.tenants.invalidate(existingOwner)
					s.kickSSE("", existingOwner)
					resp["owner"] = member
				}
			}
		} else if withOwner {
			member, err := s.addMember(org.ID, model.OrgRoleOwner, auth.CreateSubscriberRequest{
				Email: body.OwnerEmail, Password: body.OwnerPassword, DisplayName: body.OwnerName,
			})
			if err != nil {
				// The organization stays (inactive owners can be added later).
				resp["ownerError"] = err.Error()
			} else {
				resp["owner"] = member
			}
		}
		writeJSON(w, resp)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAdminOrgsUpdate changes name, live limit or active flag of an org.
func (s *HTTPServer) handleAdminOrgsUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.RequireAdmin(w, r, s.auth); !ok {
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ID       string  `json:"id"`
		Name     *string `json:"name"`
		MaxLives *int    `json:"maxLives"`
		Active   *bool   `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	org, err := s.controller.Repository().UpdateOrganization(body.ID, body.Name, body.MaxLives, body.Active)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	if body.Active != nil {
		s.tenants.invalidate("")
		if !*body.Active {
			s.controller.StopOrgMonitoring(org.ID)
			s.kickSSE(org.ID, "")
		}
	}
	writeJSON(w, org)
}

// handleAdminOrgMembers assigns an existing account to an organization (POST)
// or removes it from its organization (DELETE ?userId=). Platform admin only.
func (s *HTTPServer) handleAdminOrgMembers(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.RequireAdmin(w, r, s.auth); !ok {
		return
	}
	repo := s.controller.Repository()
	switch r.Method {
	case http.MethodPost:
		var body struct {
			OrgID  string `json:"orgId"`
			UserID string `json:"userId"`
			Email  string `json:"email"`
			Role   string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if strings.TrimSpace(body.UserID) == "" {
			writeError(w, http.StatusBadRequest, "userId é obrigatório")
			return
		}
		if s.admin != nil {
			profile, err := s.admin.GetProfileByID(body.UserID)
			if err != nil {
				writeError(w, http.StatusNotFound, "usuário não encontrado")
				return
			}
			body.Email = profile.Email
		}
		previous, prevErr := repo.GetMembership(body.UserID)
		member, err := repo.UpsertOrgMember(body.OrgID, body.UserID, body.Email, body.Role)
		if err != nil {
			writeOrgError(w, r, err)
			return
		}
		if prevErr == nil && previous.OrgID != member.OrgID {
			s.controller.DetachUser(previous.OrgID, body.UserID)
		}
		s.tenants.invalidate(body.UserID)
		s.kickSSE("", body.UserID)
		writeJSON(w, member)
	case http.MethodDelete:
		userID := strings.TrimSpace(r.URL.Query().Get("userId"))
		member, err := repo.GetMembership(userID)
		if err != nil {
			writeOrgError(w, r, err)
			return
		}
		if _, err := repo.DeleteOrgMember(member.OrgID, userID); err != nil {
			writeInternalError(w, r, err)
			return
		}
		s.tenants.invalidate(userID)
		s.controller.DetachUser(member.OrgID, userID)
		s.kickSSE("", userID)
		writeJSON(w, map[string]bool{"success": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAdminLivesAssign lists the sessions of the legacy organization (GET)
// or moves some of them into a customer organization (POST {orgId,
// sessionIds, liveNames, copySettings}). Platform admin only.
func (s *HTTPServer) handleAdminLivesAssign(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.RequireAdmin(w, r, s.auth)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		lives, err := s.controller.GetLives(model.DefaultOrgID, limitParam(r, 500))
		if err != nil {
			writeInternalError(w, r, err)
			return
		}
		writeJSON(w, map[string]any{"orgId": model.DefaultOrgID, "lives": lives})
	case http.MethodPost:
		var body struct {
			OrgID        string   `json:"orgId"`
			SessionIDs   []string `json:"sessionIds"`
			LiveNames    []string `json:"liveNames"`
			LiveName     string   `json:"liveName"`
			CopySettings bool     `json:"copySettings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		names := body.LiveNames
		if strings.TrimSpace(body.LiveName) != "" {
			names = append(names, body.LiveName)
		}
		result, err := s.controller.AssignLegacyLives(model.LiveAssignment{
			OrgID: body.OrgID, SessionIDs: body.SessionIDs, LiveNames: names, CopySettings: body.CopySettings,
		})
		switch {
		case err == nil:
		case errors.Is(err, model.ErrOrgNotFound):
			writeError(w, http.StatusNotFound, "organização de destino não encontrada")
			return
		case errors.Is(err, model.ErrOrgInactive):
			writeError(w, http.StatusConflict, "a organização de destino está desativada")
			return
		case errors.Is(err, model.ErrLiveSessionNotFound):
			writeError(w, http.StatusNotFound, "nenhuma live do legado encontrada para mover")
			return
		case errors.Is(err, model.ErrLiveNotLegacy):
			writeError(w, http.StatusConflict, "só é possível mover lives da organização de legado")
			return
		case errors.Is(err, model.ErrLiveBeingMonitored):
			writeError(w, http.StatusConflict, "a live ainda está sendo monitorada no legado; desconecte-a antes de mover")
			return
		default:
			writeOrgError(w, r, err)
			return
		}
		log.Printf("[View] admin %s moved %d legacy session(s) %v to organization %s (settings copied: %v)",
			user.ID, result.Moved, result.LiveNames, strings.TrimSpace(body.OrgID), result.SettingsCopied)
		writeJSON(w, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- Organization self-service (owner) ----

// handleOrg returns the organization of the caller.
func (s *HTTPServer) handleOrg(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	org, err := s.controller.Repository().GetOrganization(t.OrgID)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{
		"organization":  org,
		"role":          t.Role,
		"canManage":     t.CanManageOrg(),
		"platformAdmin": t.PlatformAdmin,
		"activeLives":   len(s.controller.GetLiveStates(t.OrgID)),
	})
}

// handleOrgMembers lists (GET) or creates (POST) members of the caller's
// organization. Only the owner (or a platform admin) manages members.
func (s *HTTPServer) handleOrgMembers(w http.ResponseWriter, r *http.Request) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		members, err := s.controller.Repository().ListOrgMembers(t.OrgID)
		if err != nil {
			writeInternalError(w, r, err)
			return
		}
		writeJSON(w, map[string]any{"members": members})
	case http.MethodPost:
		if s.admin == nil {
			writeError(w, http.StatusServiceUnavailable, "supabase admin não configurado")
			return
		}
		var body struct {
			Email       string `json:"email"`
			Password    string `json:"password"`
			DisplayName string `json:"displayName"`
			Role        string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		role := body.Role
		if role == "" {
			role = model.OrgRoleOperator
		}
		member, err := s.addMember(t.OrgID, role, auth.CreateSubscriberRequest{
			Email: body.Email, Password: body.Password, DisplayName: body.DisplayName,
		})
		if err != nil {
			writeOrgError(w, r, err)
			return
		}
		writeJSON(w, member)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// orgMemberOf returns the membership of userID when it belongs to orgID.
// Members of other organizations answer ErrOrgNotFound (404), never 403, so
// the caller cannot probe foreign accounts.
func (s *HTTPServer) orgMemberOf(orgID, userID string) (model.OrgMember, error) {
	member, err := s.controller.Repository().GetMembership(userID)
	if err != nil {
		return model.OrgMember{}, err
	}
	if member.OrgID != orgID {
		return model.OrgMember{}, model.ErrOrgNotFound
	}
	return member, nil
}

// protectPlatformAdmin refuses owner operations on platform admin accounts:
// an organization owner must never demote or delete a platform admin that
// happens to be a member of the organization.
func (s *HTTPServer) protectPlatformAdmin(w http.ResponseWriter, userID string) bool {
	if s.admin == nil {
		return true
	}
	profile, err := s.admin.GetProfileByID(userID)
	if err == nil && profile.Role == "admin" {
		writeError(w, http.StatusForbidden, "contas de administrador da plataforma não podem ser alteradas aqui")
		return false
	}
	return true
}

// ownerCount counts the owners of an organization.
func (s *HTTPServer) ownerCount(orgID string) (int, error) {
	members, err := s.controller.Repository().ListOrgMembers(orgID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range members {
		if m.Role == model.OrgRoleOwner {
			n++
		}
	}
	return n, nil
}

// handleOrgMembersUpdate changes the role of a member of the organization.
func (s *HTTPServer) handleOrgMembersUpdate(w http.ResponseWriter, r *http.Request) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.UserID == t.UserID {
		writeError(w, http.StatusForbidden, "não é possível alterar o próprio papel")
		return
	}
	if !model.ValidOrgRole(body.Role) {
		writeError(w, http.StatusBadRequest, "papel inválido (use owner ou operator)")
		return
	}
	member, err := s.orgMemberOf(t.OrgID, body.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "membro não encontrado")
		return
	}
	if !s.protectPlatformAdmin(w, member.UserID) {
		return
	}
	if member.Role == model.OrgRoleOwner && body.Role != model.OrgRoleOwner {
		if n, err := s.ownerCount(t.OrgID); err != nil {
			writeInternalError(w, r, err)
			return
		} else if n <= 1 {
			writeError(w, http.StatusConflict, "a organização precisa de pelo menos um dono")
			return
		}
	}
	updated, err := s.controller.Repository().UpsertOrgMember(t.OrgID, member.UserID, member.Email, body.Role)
	if err != nil {
		writeOrgError(w, r, err)
		return
	}
	s.tenants.invalidate(member.UserID)
	writeJSON(w, updated)
}

// handleOrgMembersDelete removes a member from the organization and deletes
// the account (operators exist only inside their organization).
func (s *HTTPServer) handleOrgMembersDelete(w http.ResponseWriter, r *http.Request) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("userId"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userId é obrigatório")
		return
	}
	if userID == t.UserID {
		writeError(w, http.StatusForbidden, "não é possível remover a própria conta")
		return
	}
	member, err := s.orgMemberOf(t.OrgID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "membro não encontrado")
		return
	}
	if !s.protectPlatformAdmin(w, member.UserID) {
		return
	}
	if member.Role == model.OrgRoleOwner {
		if n, err := s.ownerCount(t.OrgID); err != nil {
			writeInternalError(w, r, err)
			return
		} else if n <= 1 {
			writeError(w, http.StatusConflict, "a organização precisa de pelo menos um dono")
			return
		}
	}
	if _, err := s.controller.Repository().DeleteOrgMember(t.OrgID, userID); err != nil {
		writeInternalError(w, r, err)
		return
	}
	s.tenants.invalidate(userID)
	s.controller.DetachUser(t.OrgID, userID)
	s.kickSSE("", userID)
	if s.admin != nil {
		// Without a membership the account can no longer act; deleting it
		// also revokes its sessions. A failure here leaves a harmless
		// organization-less account behind.
		if err := s.admin.DeleteSubscriber(userID); err != nil {
			writeJSON(w, map[string]any{"success": true, "accountDeleted": false})
			return
		}
	}
	writeJSON(w, map[string]any{"success": true, "accountDeleted": s.admin != nil})
}
