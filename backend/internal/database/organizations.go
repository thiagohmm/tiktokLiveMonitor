package database

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// maxOrgNameRunes bounds an organization name.
const maxOrgNameRunes = 80

// maxOrgLives bounds the concurrent lives an organization may be granted.
const maxOrgLives = 50

// newOrgID returns a random UUID v4.
func newOrgID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate organization id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func normalizeOrgName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("nome da organização é obrigatório")
	}
	if len([]rune(name)) > maxOrgNameRunes {
		return "", fmt.Errorf("nome da organização deve ter no máximo %d caracteres", maxOrgNameRunes)
	}
	return name, nil
}

func validOrgLives(n int) error {
	if n < 1 || n > maxOrgLives {
		return fmt.Errorf("limite de lives deve estar entre 1 e %d", maxOrgLives)
	}
	return nil
}

const orgSelect = `SELECT o.id, o.name, o.max_lives, o.active, o.created_at,
	(SELECT COUNT(*) FROM organization_members m WHERE m.org_id = o.id)
	FROM organizations o`

func scanOrganization(row interface{ Scan(...any) error }) (model.Organization, error) {
	var (
		o       model.Organization
		created time.Time
	)
	if err := row.Scan(&o.ID, &o.Name, &o.MaxLives, &o.Active, &created, &o.Members); err != nil {
		return model.Organization{}, err
	}
	o.CreatedAt = created.UTC().Format(time.RFC3339)
	return o, nil
}

// CreateOrganization stores a new active organization.
func (db *DB) CreateOrganization(name string, maxLives int) (model.Organization, error) {
	name, err := normalizeOrgName(name)
	if err != nil {
		return model.Organization{}, err
	}
	if maxLives == 0 {
		maxLives = model.DefaultOrgMaxLives
	}
	if err := validOrgLives(maxLives); err != nil {
		return model.Organization{}, err
	}
	id, err := newOrgID()
	if err != nil {
		return model.Organization{}, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	if _, err := db.exec(
		`INSERT INTO organizations (id, name, max_lives) VALUES (?, ?, ?)`, id, name, maxLives); err != nil {
		return model.Organization{}, fmt.Errorf("insert organization: %w", err)
	}
	return db.getOrganizationLocked(id)
}

// GetOrganization returns one organization.
func (db *DB) GetOrganization(id string) (model.Organization, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.Organization{}, model.ErrOrgNotFound
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.getOrganizationLocked(id)
}

func (db *DB) getOrganizationLocked(id string) (model.Organization, error) {
	o, err := scanOrganization(db.queryRow(orgSelect+` WHERE o.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Organization{}, model.ErrOrgNotFound
	}
	if err != nil {
		return model.Organization{}, fmt.Errorf("query organization: %w", err)
	}
	return o, nil
}

// ListOrganizations returns every organization, by name.
func (db *DB) ListOrganizations() ([]model.Organization, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(orgSelect + ` ORDER BY LOWER(o.name), o.id`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer closeRows(rows)

	out := make([]model.Organization, 0)
	for rows.Next() {
		o, err := scanOrganization(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpdateOrganization changes the given fields (nil keeps the current value).
func (db *DB) UpdateOrganization(id string, name *string, maxLives *int, active *bool) (model.Organization, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.Organization{}, model.ErrOrgNotFound
	}
	sets := make([]string, 0, 3)
	args := make([]any, 0, 4)
	if name != nil {
		n, err := normalizeOrgName(*name)
		if err != nil {
			return model.Organization{}, err
		}
		sets = append(sets, "name = ?")
		args = append(args, n)
	}
	if maxLives != nil {
		if err := validOrgLives(*maxLives); err != nil {
			return model.Organization{}, err
		}
		sets = append(sets, "max_lives = ?")
		args = append(args, *maxLives)
	}
	if active != nil {
		if !*active && id == model.DefaultOrgID {
			return model.Organization{}, fmt.Errorf("a organização de legado não pode ser desativada")
		}
		sets = append(sets, "active = ?")
		args = append(args, *active)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	if len(sets) > 0 {
		args = append(args, id)
		res, err := db.exec(`UPDATE organizations SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
		if err != nil {
			return model.Organization{}, fmt.Errorf("update organization: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return model.Organization{}, model.ErrOrgNotFound
		}
	}
	return db.getOrganizationLocked(id)
}

const orgMemberSelect = `SELECT org_id, user_id, email, role, created_at FROM organization_members`

func scanOrgMember(row interface{ Scan(...any) error }) (model.OrgMember, error) {
	var (
		m       model.OrgMember
		created time.Time
	)
	if err := row.Scan(&m.OrgID, &m.UserID, &m.Email, &m.Role, &created); err != nil {
		return model.OrgMember{}, err
	}
	m.CreatedAt = created.UTC().Format(time.RFC3339)
	return m, nil
}

// GetMembership returns the organization membership of a user.
func (db *DB) GetMembership(userID string) (model.OrgMember, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return model.OrgMember{}, model.ErrOrgNotFound
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	m, err := scanOrgMember(db.queryRow(orgMemberSelect+` WHERE user_id = ?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.OrgMember{}, model.ErrOrgNotFound
	}
	if err != nil {
		return model.OrgMember{}, fmt.Errorf("query membership: %w", err)
	}
	return m, nil
}

// ListOrgMembers returns the members of one organization.
func (db *DB) ListOrgMembers(orgID string) ([]model.OrgMember, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return nil, model.ErrOrgRequired
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(orgMemberSelect+` WHERE org_id = ? ORDER BY role, LOWER(email), user_id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
	}
	defer closeRows(rows)

	out := make([]model.OrgMember, 0)
	for rows.Next() {
		m, err := scanOrgMember(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertOrgMember assigns a user to an organization with the given role.
func (db *DB) UpsertOrgMember(orgID, userID, email, role string) (model.OrgMember, error) {
	orgID = strings.TrimSpace(orgID)
	userID = strings.TrimSpace(userID)
	role = strings.TrimSpace(role)
	if orgID == "" {
		return model.OrgMember{}, model.ErrOrgRequired
	}
	if userID == "" {
		return model.OrgMember{}, fmt.Errorf("user id is required")
	}
	if !model.ValidOrgRole(role) {
		return model.OrgMember{}, fmt.Errorf("papel inválido: %q", role)
	}
	if orgID == model.DefaultOrgID {
		return model.OrgMember{}, fmt.Errorf("a organização de legado não aceita membros (é exclusiva do admin da plataforma)")
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	if _, err := db.getOrganizationLocked(orgID); err != nil {
		return model.OrgMember{}, err
	}
	if _, err := db.exec(
		`INSERT INTO organization_members (org_id, user_id, email, role) VALUES (?, ?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET org_id = EXCLUDED.org_id, email = EXCLUDED.email, role = EXCLUDED.role`,
		orgID, userID, strings.TrimSpace(email), role); err != nil {
		return model.OrgMember{}, fmt.Errorf("upsert organization member: %w", err)
	}
	return scanOrgMember(db.queryRow(orgMemberSelect+` WHERE user_id = ?`, userID))
}

// maxAssignItems bounds the session ids and live names of one assignment.
const maxAssignItems = 500

// uniqueTrimmed trims values, dropping empty and repeated ones.
func uniqueTrimmed(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func inPlaceholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// AssignLegacyLives moves sessions of the legacy organization into a customer
// organization, in one transaction. Event rows follow on their own: every
// event table is scoped by live_id -> live_sessions.org_id.
//
// Only the legacy organization is a valid source (customer data never changes
// owner); every explicit session id must be a legacy session, otherwise
// nothing moves. The destination must exist, be active and not be the legacy
// organization. Moved sessions are closed, so the destination never resumes a
// stale legacy session as its current live. With CopySettings the legacy
// settings replace the destination's.
func (db *DB) AssignLegacyLives(a model.LiveAssignment) (model.LiveAssignResult, error) {
	orgID, err := requireOrg(a.OrgID)
	if err != nil {
		return model.LiveAssignResult{}, err
	}
	if orgID == model.DefaultOrgID {
		return model.LiveAssignResult{}, fmt.Errorf("a organização de destino não pode ser a de legado")
	}
	ids := uniqueTrimmed(a.SessionIDs)
	names := uniqueTrimmed(a.LiveNames)
	if len(ids) == 0 && len(names) == 0 {
		return model.LiveAssignResult{}, fmt.Errorf("informe as sessões (sessionIds) ou as lives (liveNames) a mover")
	}
	if len(ids) > maxAssignItems || len(names) > maxAssignItems {
		return model.LiveAssignResult{}, fmt.Errorf("no máximo %d sessões ou lives por vez", maxAssignItems)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return model.LiveAssignResult{}, fmt.Errorf("update assign lives (begin): %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// FOR UPDATE: a concurrent deactivation waits for this move to finish.
	var active bool
	err = tx.QueryRow(db.bind(`SELECT active FROM organizations WHERE id = ? FOR UPDATE`), orgID).Scan(&active)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return model.LiveAssignResult{}, model.ErrOrgNotFound
	case err != nil:
		return model.LiveAssignResult{}, fmt.Errorf("query destination organization: %w", err)
	case !active:
		return model.LiveAssignResult{}, model.ErrOrgInactive
	}

	if len(ids) > 0 {
		args := make([]any, 0, len(ids))
		for _, id := range ids {
			args = append(args, id)
		}
		rows, err := tx.Query(db.bind(
			`SELECT id, org_id FROM live_sessions WHERE id IN (`+inPlaceholders(len(ids))+`) FOR UPDATE`), args...)
		if err != nil {
			return model.LiveAssignResult{}, fmt.Errorf("query sessions to assign: %w", err)
		}
		owners := make(map[string]string, len(ids))
		for rows.Next() {
			var id, owner string
			if err := rows.Scan(&id, &owner); err != nil {
				_ = rows.Close()
				return model.LiveAssignResult{}, fmt.Errorf("query sessions to assign (scan): %w", err)
			}
			owners[id] = owner
		}
		if err := rows.Close(); err != nil {
			return model.LiveAssignResult{}, fmt.Errorf("query sessions to assign: %w", err)
		}
		for _, id := range ids {
			owner, ok := owners[id]
			if !ok {
				return model.LiveAssignResult{}, model.ErrLiveSessionNotFound
			}
			if owner != model.DefaultOrgID {
				return model.LiveAssignResult{}, model.ErrLiveNotLegacy
			}
		}
	}

	conds := make([]string, 0, 2)
	args := []any{orgID, model.DefaultOrgID}
	if len(ids) > 0 {
		conds = append(conds, `id IN (`+inPlaceholders(len(ids))+`)`)
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if len(names) > 0 {
		conds = append(conds, `live_name IN (`+inPlaceholders(len(names))+`)`)
		for _, n := range names {
			args = append(args, n)
		}
	}
	rows, err := tx.Query(db.bind(
		`UPDATE live_sessions SET org_id = ?, ended_at = COALESCE(ended_at, last_seen_at)
		 WHERE org_id = ? AND (`+strings.Join(conds, " OR ")+`)
		 RETURNING live_name`), args...)
	if err != nil {
		return model.LiveAssignResult{}, fmt.Errorf("update sessions organization: %w", err)
	}
	result := model.LiveAssignResult{LiveNames: []string{}}
	movedNames := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return model.LiveAssignResult{}, fmt.Errorf("update sessions organization (scan): %w", err)
		}
		result.Moved++
		if _, ok := movedNames[name]; !ok {
			movedNames[name] = struct{}{}
			result.LiveNames = append(result.LiveNames, name)
		}
	}
	if err := rows.Close(); err != nil {
		return model.LiveAssignResult{}, fmt.Errorf("update sessions organization: %w", err)
	}
	if result.Moved == 0 {
		return model.LiveAssignResult{}, model.ErrLiveSessionNotFound
	}

	if a.CopySettings {
		res, err := tx.Exec(db.bind(
			`INSERT INTO settings (key, value) SELECT ?, value FROM settings WHERE key = ?
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`),
			OrgSettingsKey(orgID), OrgSettingsKey(model.DefaultOrgID))
		if err != nil {
			return model.LiveAssignResult{}, fmt.Errorf("update copy legacy settings: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			result.SettingsCopied = true
		}
	}

	if err := tx.Commit(); err != nil {
		return model.LiveAssignResult{}, fmt.Errorf("update commit assign lives: %w", err)
	}
	return result, nil
}

// DeleteOrgMember removes a user from an organization. Returns false when the
// user was not a member of orgID.
func (db *DB) DeleteOrgMember(orgID, userID string) (bool, error) {
	orgID = strings.TrimSpace(orgID)
	userID = strings.TrimSpace(userID)
	if orgID == "" || userID == "" {
		return false, model.ErrOrgRequired
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	res, err := db.exec(`DELETE FROM organization_members WHERE org_id = ? AND user_id = ?`, orgID, userID)
	if err != nil {
		return false, fmt.Errorf("delete organization member: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete organization member rows affected: %w", err)
	}
	return n > 0, nil
}
