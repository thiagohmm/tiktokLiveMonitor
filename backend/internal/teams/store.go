// Package teams implements tenant-scoped seats, invitations and manual subscriptions.
package teams

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
)

var (
	ErrLastOwner      = errors.New("a organização precisa de pelo menos um dono")
	ErrNotFound       = errors.New("recurso não encontrado")
	ErrCapacity       = errors.New("não há vaga disponível; contrate uma vaga extra")
	ErrSuspended      = errors.New("a vaga adicional está suspensa; solicite a renovação ao dono")
	ErrRegularization = errors.New("regularize a equipe antes de convidar novos membros")
)

// Store serializes team changes through a row lock on the organization.
type Store struct{ DB *sql.DB }

func New(db *sql.DB) *Store { return &Store{DB: db} }

// Seat describes an additional position, independent of its occupant's role.
type Seat struct {
	Number    int    `json:"number"`
	UserID    string `json:"userId"`
	Included  bool   `json:"included"`
	Suspended bool   `json:"suspended"`
}
type Summary struct {
	Enabled      bool       `json:"enabled"`
	PrimaryOwner string     `json:"primaryOwner"`
	Included     int        `json:"included"`
	Extra        int        `json:"extra"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	PriceCents   int64      `json:"priceCents"`
	Seats        []Seat     `json:"seats"`
	Pending      int        `json:"pending"`
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
type Payment struct {
	Reference   string    `json:"reference"`
	Quantity    int       `json:"quantity"`
	AmountCents int64     `json:"amountCents"`
	StartsAt    time.Time `json:"startsAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Kind        string    `json:"kind"`
	Notes       string    `json:"notes"`
}

type orgLock struct {
	Primary string
	Enabled bool
	Extra   int
	Expires time.Time
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
	var start time.Time
	err = tx.QueryRowContext(ctx, `SELECT quantity,starts_at,expires_at FROM organization_seat_subscriptions WHERE org_id=$1 AND starts_at<=now() AND expires_at>now() ORDER BY expires_at DESC LIMIT 1`, org).Scan(&o.Extra, &start, &o.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	if time.Now().Before(start) {
		o.Extra = 0
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
	_, err := tx.ExecContext(ctx, `UPDATE organization_invitations SET status='expired' WHERE org_id=$1 AND status='pending' AND (expires_at<=now() OR (seat_number>2 AND NOT EXISTS(SELECT 1 FROM organization_seat_subscriptions WHERE org_id=$1 AND starts_at<=now() AND expires_at>now() AND quantity>=seat_number-2)))`, org)
	return err
}

// Access reads current membership and entitlement; no cached claims grant access.
func (s *Store) Access(ctx context.Context, org, user string) error {
	var valid bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members m JOIN organizations o ON o.id=m.org_id WHERE m.org_id=$1 AND m.user_id=$2 AND o.active AND (NOT o.team_rules_enabled OR o.primary_owner_user_id=m.user_id OR EXISTS(SELECT 1 FROM organization_seats v WHERE v.org_id=o.id AND v.user_id=m.user_id AND (v.number<=2 OR EXISTS(SELECT 1 FROM organization_seat_subscriptions p WHERE p.org_id=o.id AND p.starts_at<=now() AND p.expires_at>now() AND p.quantity>=v.number-2)))))`, org, user).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrSuspended
	}
	return nil
}

// Summary returns only one tenant's capacity and allocations.
func (s *Store) Summary(ctx context.Context, org string) (Summary, error) {
	o := Summary{Included: 2, Seats: []Seat{}}
	err := s.DB.QueryRowContext(ctx, `SELECT team_rules_enabled,COALESCE(primary_owner_user_id,'') FROM organizations WHERE id=$1`, org).Scan(&o.Enabled, &o.PrimaryOwner)
	if err != nil {
		return o, err
	}
	var start, end time.Time
	err = s.DB.QueryRowContext(ctx, `SELECT quantity,starts_at,expires_at FROM organization_seat_subscriptions WHERE org_id=$1 AND starts_at<=now() AND expires_at>now() ORDER BY expires_at DESC LIMIT 1`, org).Scan(&o.Extra, &start, &end)
	if err == nil {
		o.ExpiresAt = &end
	} else if !errors.Is(err, sql.ErrNoRows) {
		return o, err
	}
	var price string
	err = s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='billing:seat_price_cents'`).Scan(&price)
	if err == nil {
		if _, err = fmt.Sscan(price, &o.PriceCents); err != nil {
			return o, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
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
		seat.Included = seat.Number <= 2
		seat.Suspended = !seat.Included && (time.Now().Before(start) || !time.Now().Before(end) || seat.Number > 2+o.Extra)
		o.Seats = append(o.Seats, seat)
	}
	if err = rows.Err(); err != nil {
		return o, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM organization_invitations WHERE org_id=$1 AND status='pending' AND expires_at>now() AND (seat_number<=2 OR EXISTS(SELECT 1 FROM organization_seat_subscriptions p WHERE p.org_id=$1 AND p.starts_at<=now() AND p.expires_at>now() AND p.quantity>=seat_number-2))`, org).Scan(&o.Pending)
	return o, err
}

// CreateInvitation reserves one vacant, paid-or-included seat.
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
	capacity := 2
	if time.Now().Before(o.Expires) {
		capacity += o.Extra
	}
	var number int
	err = tx.QueryRowContext(ctx, `SELECT number FROM organization_seats v WHERE org_id=$1 AND number<=$2 AND user_id IS NULL AND NOT EXISTS(SELECT 1 FROM organization_invitations i WHERE i.org_id=v.org_id AND i.seat_number=v.number AND i.status='pending') ORDER BY number LIMIT 1`, org, capacity).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, "", ErrCapacity
	}
	if err != nil {
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
	if seat > 2 && (!time.Now().Before(o.Expires) || seat > 2+o.Extra) {
		return "", ErrSuspended
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
		return "", ErrCapacity
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
