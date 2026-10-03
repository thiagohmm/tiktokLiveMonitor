package teams

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
)

// Allocation fixes the responsible owner and explicitly assigns every additional member.
type Allocation struct {
	PrimaryOwner string `json:"primaryOwner"`
	Seats        []Seat `json:"seats"`
}

// Allocate regularizes a team or swaps allocations without creating capacity.
func (s *Store) Allocate(ctx context.Context, org, actor string, a Allocation, platformAdmin bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	o, err := lockOrg(ctx, tx, org)
	if err != nil {
		return err
	}
	if !platformAdmin && (!o.Enabled || a.PrimaryOwner != o.Primary) {
		return errors.New("apenas o administrador transfere o dono principal ou ativa regras")
	}
	var role string
	if err = tx.QueryRowContext(ctx, `SELECT role FROM organization_members WHERE org_id=$1 AND user_id=$2`, org, a.PrimaryOwner).Scan(&role); err != nil || role != "owner" {
		return errors.New("dono principal deve ser um dono da organização")
	}
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM organization_members WHERE org_id=$1 AND user_id<>$2`, org, a.PrimaryOwner)
	if err != nil {
		return err
	}
	users := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			closeRows(rows)
			return err
		}
		users[id] = true
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return err
	}
	if len(users) != len(a.Seats) {
		return errors.New("informe uma vaga para cada membro adicional")
	}
	seen := map[int]bool{}
	for _, seat := range a.Seats {
		if !users[seat.UserID] || seen[seat.Number] || seat.Number < 1 {
			return errors.New("alocação inválida ou duplicada")
		}
		if seat.Number > 2+o.Extra && !o.Enabled {
			return errors.New("regularize as vagas extras antes de ativar a equipe")
		}
		delete(users, seat.UserID)
		seen[seat.Number] = true
	}
	if err = expireInvites(ctx, tx, org); err != nil {
		return err
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM organization_invitations WHERE org_id=$1 AND status='pending'`, org).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return errors.New("cancele os convites pendentes antes de realocar vagas")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_seats SET user_id=NULL WHERE org_id=$1`, org); err != nil {
		return err
	}
	for _, seat := range a.Seats {
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_seats(org_id,number,user_id) VALUES($1,$2,$3) ON CONFLICT(org_id,number) DO UPDATE SET user_id=excluded.user_id`, org, seat.Number, seat.UserID); err != nil {
			return err
		}
	}
	for n := 1; n <= 2+o.Extra; n++ {
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_seats(org_id,number) VALUES($1,$2) ON CONFLICT DO NOTHING`, org, n); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organizations SET primary_owner_user_id=$2,team_rules_enabled=true WHERE id=$1`, org, a.PrimaryOwner); err != nil {
		return err
	}
	if err = audit(ctx, tx, org, actor, "team.allocated", map[string]any{"beforePrimary": o.Primary, "allocation": a}); err != nil {
		return err
	}
	return tx.Commit()
}

// SetRole changes an organization role without changing its paid allocation.
func (s *Store) SetRole(ctx context.Context, org, actor, user, role string) error {
	if role != "owner" && role != "operator" {
		return errors.New("papel inválido")
	}
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
		return errors.New("não é possível alterar o dono principal ou o próprio papel")
	}
	var before string
	err = tx.QueryRowContext(ctx, `SELECT m.role FROM organization_members m LEFT JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 AND m.user_id=$2 AND COALESCE(u.role,'subscriber')<>'admin' FOR UPDATE OF m`, org, user).Scan(&before)
	if err != nil {
		return ErrNotFound
	}
	if before == "owner" && role != "owner" {
		var owners int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM organization_members WHERE org_id=$1 AND role='owner'`, org).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_members SET role=$3 WHERE org_id=$1 AND user_id=$2`, org, user, role); err != nil {
		return err
	}
	if err = audit(ctx, tx, org, actor, "member.role", map[string]string{"userId": user, "before": before, "after": role}); err != nil {
		return err
	}
	return tx.Commit()
}

// SetPrice records the global monthly unit price in cents.
func (s *Store) SetPrice(ctx context.Context, actor string, price int64) error {
	if price <= 0 || price > 100000000 {
		return errors.New("preço inválido")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var before string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='billing:seat_price_cents' FOR UPDATE`).Scan(&before)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('billing:seat_price_cents',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(price)); err != nil {
		return err
	}
	if err = audit(ctx, tx, "platform", actor, "billing.price", map[string]any{"before": before, "after": price}); err != nil {
		return err
	}
	return tx.Commit()
}

// AddCalendarMonth clamps the day to the final day of the following month in São Paulo.
func AddCalendarMonth(at time.Time) time.Time {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.FixedZone("BRT", -3*3600)
	}
	at = at.In(loc)
	first := time.Date(at.Year(), at.Month()+1, 1, at.Hour(), at.Minute(), at.Second(), at.Nanosecond(), loc)
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, loc).Day()
	day := at.Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, at.Hour(), at.Minute(), at.Second(), at.Nanosecond(), loc).UTC()
}

// RecordPayment schedules a non-overlapping subscription period; retries never duplicate it.
func (s *Store) RecordPayment(ctx context.Context, org, actor string, p Payment) error {
	if p.Reference == "" || p.Quantity < 0 || p.Quantity > 1000 || p.AmountCents < 0 {
		return errors.New("referência, quantidade ou valor inválido")
	}
	if p.Kind == "" {
		p.Kind = "payment"
	}
	if p.Kind != "payment" && p.Kind != "temporary" {
		return errors.New("tipo inválido")
	}
	if p.Kind == "temporary" && strings.TrimSpace(p.Notes) == "" {
		return errors.New("liberação temporária exige justificativa")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = lockOrg(ctx, tx, org); err != nil {
		return err
	}
	if err := expireInvites(ctx, tx, org); err != nil {
		return err
	}
	var duplicate bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_seat_payments WHERE reference=$1)`, p.Reference).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return errors.New("referência de pagamento já registrada")
	}
	var latest sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT max(expires_at) FROM organization_seat_subscriptions WHERE org_id=$1`, org).Scan(&latest); err != nil {
		return err
	}
	if p.StartsAt.IsZero() {
		p.StartsAt = time.Now()
		if latest.Valid && latest.Time.After(p.StartsAt) {
			p.StartsAt = latest.Time
		}
	}
	if p.ExpiresAt.IsZero() {
		p.ExpiresAt = AddCalendarMonth(p.StartsAt)
	}
	if !p.ExpiresAt.After(p.StartsAt) {
		return errors.New("vencimento deve ser posterior ao início")
	}
	var overlap bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_seat_subscriptions WHERE org_id=$1 AND starts_at<$3 AND expires_at>$2)`, org, p.StartsAt, p.ExpiresAt).Scan(&overlap); err != nil {
		return err
	}
	if overlap {
		return errors.New("período sobreposto; mudanças de quantidade entram no próximo período")
	}
	var price int64
	if p.Kind == "payment" {
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='billing:seat_price_cents'`).Scan(&raw); err != nil {
			return errors.New("configure o preço por vaga primeiro")
		}
		if _, err = fmt.Sscan(raw, &price); err != nil || price <= 0 {
			return errors.New("preço não configurado")
		}
		if p.AmountCents != int64(p.Quantity)*price {
			return errors.New("valor deve corresponder à quantidade × preço por vaga")
		}
	}
	// A smaller renewal may not silently pick which members keep access.
	var excess int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM organization_seats WHERE org_id=$1 AND number>$2 AND user_id IS NOT NULL`, org, p.Quantity+2).Scan(&excess); err != nil {
		return err
	}
	if excess > 0 {
		return errors.New("realocar ou revogar membros das vagas removidas antes de reduzir a quantidade")
	}
	id := auth.RandomToken()
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_seat_payments(id,org_id,reference,quantity,price_cents,amount_cents,starts_at,expires_at,kind,notes,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, org, p.Reference, p.Quantity, price, p.AmountCents, p.StartsAt, p.ExpiresAt, p.Kind, p.Notes, actor); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_seat_subscriptions(id,org_id,quantity,starts_at,expires_at) VALUES($1,$2,$3,$4,$5)`, id, org, p.Quantity, p.StartsAt, p.ExpiresAt); err != nil {
		return err
	}
	for n := 1; n <= p.Quantity+2; n++ {
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_seats(org_id,number) VALUES($1,$2) ON CONFLICT DO NOTHING`, org, n); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, org, actor, "billing.recorded", p); err != nil {
		return err
	}
	return tx.Commit()
}

// Payments returns the immutable billing ledger, including scheduled renewals.
func (s *Store) Payments(ctx context.Context, org string) ([]Payment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT reference,quantity,amount_cents,starts_at,expires_at,kind,notes FROM organization_seat_payments WHERE org_id=$1 ORDER BY starts_at DESC LIMIT 100`, org)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := []Payment{}
	for rows.Next() {
		var p Payment
		if err = rows.Scan(&p.Reference, &p.Quantity, &p.AmountCents, &p.StartsAt, &p.ExpiresAt, &p.Kind, &p.Notes); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9_.]{1,64}$`)

func NormalizeUsername(name string) (string, error) {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "@"))
	if !usernamePattern.MatchString(name) {
		return "", errors.New("username TikTok inválido")
	}
	return name, nil
}

// AllowedLives returns a tenant's registered TikTok identities.
func (s *Store) AllowedLives(ctx context.Context, org string) ([]AllowedLive, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT username,active FROM organization_allowed_lives WHERE org_id=$1 ORDER BY username`, org)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := []AllowedLive{}
	for rows.Next() {
		var l AllowedLive
		if err = rows.Scan(&l.Username, &l.Active); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetAllowedLive records an audited administrative allowlist change.
func (s *Store) SetAllowedLive(ctx context.Context, org, actor string, l AllowedLive) error {
	name, err := NormalizeUsername(l.Username)
	if err != nil {
		return err
	}
	l.Username = name
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = lockOrg(ctx, tx, org); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_allowed_lives(org_id,username,active) VALUES($1,$2,$3) ON CONFLICT(org_id,username) DO UPDATE SET active=excluded.active`, org, name, l.Active); err != nil {
		return err
	}
	if err = audit(ctx, tx, org, actor, "live.authorization", l); err != nil {
		return err
	}
	return tx.Commit()
}

// Audit returns recent tenant-scoped actions with their recorded changes.
func (s *Store) Audit(ctx context.Context, org string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT actor_id,action,data,created_at FROM organization_audit_logs WHERE org_id=$1 ORDER BY id DESC LIMIT 100`, org)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := []map[string]any{}
	for rows.Next() {
		var actor, action string
		var raw []byte
		var at time.Time
		if err = rows.Scan(&actor, &action, &raw, &at); err != nil {
			return nil, err
		}
		var data any
		if err = json.Unmarshal(raw, &data); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"actor": actor, "action": action, "data": data, "createdAt": at})
	}
	return out, rows.Err()
}
