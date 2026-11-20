// Package teams implements tenant-scoped seats and invitations.
package teams

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
)

var (
	ErrLastOwner      = errors.New("a organização precisa de pelo menos um dono")
	ErrNotFound       = errors.New("recurso não encontrado")
	ErrRegularization = errors.New("regularize a equipe antes de convidar novos membros")
)

// Store serializes team changes through a row lock on the organization.
type Store struct{ DB *sql.DB }

func New(db *sql.DB) *Store { return &Store{DB: db} }

// Seat describes a team position, independent of its occupant's role.
type Seat struct {
	Number int    `json:"number"`
	UserID string `json:"userId"`
}
type Summary struct {
	Enabled      bool     `json:"enabled"`
	PrimaryOwner string   `json:"primaryOwner"`
	Seats        []Seat   `json:"seats"`
	Pending      int      `json:"pending"`
}
type Invitation struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Seat      int       `json:"seat"`
	Status    string    `json:"status"`
	Delivery  string    `json:"delivery"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type AllowedLive struct {
	Username string `json:"username"`
	Active   bool   `json:"active"`
}

type orgLock struct {
	Primary string
	Enabled bool
}

func lockOrg(ctx context.Context, tx *sql.Tx, org string) (orgLock, error) {
	var o orgLock
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(primary_owner_user_id,''),team_rules_enabled,active FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&o.Primary, &o.Enabled, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	if err != nil {
		return o, err
	}
	if !active {
		return o, errors.New("organização desativada")
	}
	return o, nil
}
func audit(ctx context.Context, tx *sql.Tx, org, actor, action string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_audit_logs(org_id,actor_id,action,data) VALUES($1,$2,$3,$4)`, org, actor, action, string(b))
	return err
}
func expireInvites(ctx context.Context, tx *sql.Tx, org string) error {
	_, err := tx.ExecContext(ctx, `UPDATE organization_invitations SET status='expired' WHERE org_id=$1 AND status='pending' AND expires_at<=now()`, org)
	return err
}

// Summary returns only one tenant's seats and allocations.
func (s *Store) Summary(ctx context.Context, org string) (Summary, error) {
	o := Summary{Seats: []Seat{}}
	err := s.DB.QueryRowContext(ctx, `SELECT team_rules_enabled,COALESCE(primary_owner_user_id,'') FROM organizations WHERE id=$1`, org).Scan(&o.Enabled, &o.PrimaryOwner)
	if err != nil {
		return o, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT number,COALESCE(user_id,'') FROM organization_seats WHERE org_id=$1 ORDER BY number`, org)
	if err != nil {
		return o, err
	}
	defer closeRows(rows)
	for rows.Next() {
		var seat Seat
		if err = rows.Scan(&seat.Number, &seat.UserID); err != nil {
			return o, err
		}
		o.Seats = append(o.Seats, seat)
	}
	if err = rows.Err(); err != nil {
		return o, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM organization_invitations WHERE org_id=$1 AND status='pending' AND expires_at>now()`, org).Scan(&o.Pending)
	return o, err
}

// CreateInvitation reserves one vacant seat, creating a new position when
// the existing ones are all occupied or reserved.
func (s *Store) CreateInvitation(ctx context.Context, org, actor, email string) (Invitation, string, error) {
	email, err := auth.NormalizeEmail(email)
	if err != nil {
		return Invitation{}, "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Invitation{}, "", err
	}
	defer rollback(tx)
	o, err := lockOrg(ctx, tx, org)
	if err != nil {
		return Invitation{}, "", err
	}
	if !o.Enabled {
		return Invitation{}, "", ErrRegularization
	}
	if err = expireInvites(ctx, tx, org); err != nil {
		return Invitation{}, "", err
	}
	var member bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE org_id=$1 AND lower(email)=$2)`, org, email).Scan(&member); err != nil {
		return Invitation{}, "", err
	}
	if member {
		return Invitation{}, "", errors.New("já existe membro com este e-mail")
	}
	var number int
	err = tx.QueryRowContext(ctx, `SELECT number FROM organization_seats v WHERE org_id=$1 AND user_id IS NULL AND NOT EXISTS(SELECT 1 FROM organization_invitations i WHERE i.org_id=v.org_id AND i.seat_number=v.number AND i.status='pending') ORDER BY number LIMIT 1`, org).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `INSERT INTO organization_seats(org_id,number) VALUES($1,(SELECT COALESCE(max(number),0)+1 FROM organization_seats WHERE org_id=$1)) RETURNING number`, org).Scan(&number)
		if err != nil {
			return Invitation{}, "", errors.New("convite não disponível; confira os convites pendentes")
		}
	} else if err != nil {
		return Invitation{}, "", err
	}
	token := auth.RandomToken()
	i := Invitation{ID: auth.RandomToken(), Email: email, Seat: number, Status: "pending", Delivery: "pending", ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_invitations(id,org_id,email,seat_number,token_hash,expires_at,invited_by) VALUES($1,$2,$3,$4,$5,$6,$7)`, i.ID, org, email, number, auth.TokenHash(token), i.ExpiresAt, actor)
	if err != nil {
		return i, "", errors.New("convite não disponível; confira os convites pendentes")
	}
	if err = audit(ctx, tx, org, actor, "invitation.created", i); err != nil {
		return i, "", err
	}
	if err = tx.Commit(); err != nil {
		return i, "", err
	}
	return i, token, nil
}

// Invitations lists reservations for the caller's organization.
func (s *Store) Invitations(ctx context.Context, org string) ([]Invitation, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if _, err = lockOrg(ctx, tx, org); err != nil {
		return nil, err
	}
	if err = expireInvites(ctx, tx, org); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,email,seat_number,status,delivery_status,expires_at FROM organization_invitations WHERE org_id=$1 ORDER BY created_at DESC LIMIT 100`, org)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := []Invitation{}
	for rows.Next() {
		var i Invitation
		if err = rows.Scan(&i.ID, &i.Email, &i.Seat, &i.Status, &i.Delivery, &i.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ChangeInvitation revokes the previous token while retaining an eligible reservation on resend.
func (s *Store) ChangeInvitation(ctx context.Context, org, actor, id string, resend bool) (Invitation, string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Invitation{}, "", err
	}
	defer rollback(tx)
	if _, err = lockOrg(ctx, tx, org); err != nil {
		return Invitation{}, "", err
	}
	if err = expireInvites(ctx, tx, org); err != nil {
		return Invitation{}, "", err
	}
	i := Invitation{}
	err = tx.QueryRowContext(ctx, `SELECT id,email,seat_number,status,delivery_status,expires_at FROM organization_invitations WHERE org_id=$1 AND id=$2 AND status='pending' FOR UPDATE`, org, id).Scan(&i.ID, &i.Email, &i.Seat, &i.Status, &i.Delivery, &i.ExpiresAt)
	if err != nil {
		return i, "", ErrNotFound
	}
	token := ""
	action := "invitation.cancelled"
	if resend {
		token = auth.RandomToken()
		i.ExpiresAt = time.Now().Add(7 * 24 * time.Hour)
		action = "invitation.resent"
		_, err = tx.ExecContext(ctx, `UPDATE organization_invitations SET token_hash=$3,expires_at=$4,delivery_status='pending' WHERE org_id=$1 AND id=$2`, org, id, auth.TokenHash(token), i.ExpiresAt)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE organization_invitations SET status='cancelled' WHERE org_id=$1 AND id=$2`, org, id)
	}
	if err != nil {
		return i, "", err
	}
	if err = audit(ctx, tx, org, actor, action, i); err != nil {
		return i, "", err
	}
	return i, token, tx.Commit()
}

// MarkDelivery records mail transport success without storing a raw invitation token.
func (s *Store) MarkDelivery(ctx context.Context, org, id, token string, sent bool) error {
	status := "failed"
	if sent {
		status = "sent"
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE organization_invitations SET delivery_status=$4 WHERE org_id=$1 AND id=$2 AND token_hash=$3`, org, id, auth.TokenHash(token), status)
	return err
}

// AcceptInvitation creates a new identity or attaches the explicitly authenticated existing identity.
func (s *Store) AcceptInvitation(ctx context.Context, token, password, name, existingID string) (string, error) {
	var org string
	if err := s.DB.QueryRowContext(ctx, `SELECT org_id FROM organization_invitations WHERE token_hash=$1`, auth.TokenHash(token)).Scan(&org); err != nil {
		return "", ErrNotFound
	}
	var hash string
	var err error
	if existingID == "" {
		hash, err = auth.HashPassword(password)
		if err != nil {
			return "", err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer rollback(tx)
	o, err := lockOrg(ctx, tx, org)
	if err != nil {
		return "", err
	}
	if !o.Enabled {
		return "", ErrRegularization
	}
	if err = expireInvites(ctx, tx, org); err != nil {
		return "", err
	}
	var id, email string
	var seat int
	err = tx.QueryRowContext(ctx, `SELECT id,email,seat_number FROM organization_invitations WHERE org_id=$1 AND token_hash=$2 AND status='pending' AND expires_at>now() FOR UPDATE`, org, auth.TokenHash(token)).Scan(&id, &email, &seat)
	if err != nil {
		return "", errors.New("convite inválido ou expirado")
	}
	var userID string
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT id,active FROM users WHERE email=$1 FOR UPDATE`, email).Scan(&userID, &active)
	if errors.Is(err, sql.ErrNoRows) {
		if existingID != "" {
			return "", errors.New("entre com a conta correspondente ao convite")
		}
		userID = auth.RandomToken()
		if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,password_hash,display_name,active) VALUES($1,$2,$3,$4,true)`, userID, email, hash, name); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if userID != existingID || !active {
		return "", errors.New("entre com a conta correspondente ao convite; se necessário, redefina sua senha")
	}
	var member bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE user_id=$1)`, userID).Scan(&member); err != nil {
		return "", err
	}
	if member {
		return "", errors.New("esta conta já possui um vínculo; não é possível aceitar o convite")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_members(user_id,org_id,email,role) VALUES($1,$2,$3,'operator')`, userID, org, email); err != nil {
		return "", err
	}
	res, err := tx.ExecContext(ctx, `UPDATE organization_seats SET user_id=$3 WHERE org_id=$1 AND number=$2 AND user_id IS NULL`, org, seat, userID)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return "", errors.New("vaga indisponível; recarregue a lista de convites")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_invitations SET status='accepted' WHERE id=$1`, id); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET organization_revoked=false WHERE id=$1`, userID); err != nil {
		return "", err
	}
	if err = audit(ctx, tx, org, userID, "invitation.accepted", map[string]string{"invitationId": id}); err != nil {
		return "", err
	}
	return userID, tx.Commit()
}

// Revoke removes tenant access without deleting the account or history.
func (s *Store) Revoke(ctx context.Context, org, actor, user string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	o, err := lockOrg(ctx, tx, org)
	if err != nil {
		return err
	}
	if user == actor || user == o.Primary {
		return errors.New("não é possível remover o dono principal ou a própria conta")
	}
	var role string
	err = tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id=$1`, user).Scan(&role)
	if err == nil && role == "admin" {
		return errors.New("administrador da plataforma protegido")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var memberRole string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM organization_members WHERE org_id=$1 AND user_id=$2`, org, user).Scan(&memberRole); err != nil {
		return ErrNotFound
	}
	if memberRole == "owner" {
		var owners int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM organization_members WHERE org_id=$1 AND role='owner'`, org).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM organization_members WHERE org_id=$1 AND user_id=$2`, org, user)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_seats SET user_id=NULL WHERE org_id=$1 AND user_id=$2`, org, user); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET organization_revoked=true WHERE id=$1`, user); err != nil {
		return err
	}
	if err = audit(ctx, tx, org, actor, "member.revoked", map[string]string{"userId": user}); err != nil {
		return err
	}
	return tx.Commit()
}
