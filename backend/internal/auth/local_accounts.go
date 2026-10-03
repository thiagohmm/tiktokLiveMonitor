package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SubscriberProfile is the admin-facing view of a paying customer account.
type SubscriberProfile struct {
	ID                    string     `json:"id"`
	Email                 string     `json:"email"`
	DisplayName           string     `json:"displayName"`
	Role                  string     `json:"role"`
	Active                bool       `json:"active"`
	Notes                 string     `json:"notes"`
	SubscriptionExpiresAt *time.Time `json:"subscriptionExpiresAt,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

type CreateSubscriberRequest struct {
	Email                 string `json:"email"`
	Password              string `json:"password"`
	DisplayName           string `json:"displayName"`
	Notes                 string `json:"notes"`
	SubscriptionExpiresAt string `json:"subscriptionExpiresAt"`
}

// SignUpRequest is the public self-service registration payload.
// Role and active are never accepted from the client: every signup is a
// pending subscriber until an admin confirms payment.
type SignUpRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	Notes       string `json:"notes"`
}

type UpdateSubscriberRequest struct {
	ID                    string  `json:"id"`
	Password              *string `json:"password,omitempty"`
	DisplayName           *string `json:"displayName,omitempty"`
	Active                *bool   `json:"active,omitempty"`
	Notes                 *string `json:"notes,omitempty"`
	SubscriptionExpiresAt *string `json:"subscriptionExpiresAt,omitempty"`
}

// ErrDuplicateSignup indicates an existing identity; it never replaces a password.
var ErrDuplicateSignup = errors.New("e-mail já cadastrado")

const profileColumns = "id,email,display_name,role,active,notes,subscription_expires_at,created_at,updated_at"

func scanProfile(row interface{ Scan(...any) error }) (*SubscriberProfile, error) {
	var p SubscriberProfile
	var expiry sql.NullTime
	if err := row.Scan(&p.ID, &p.Email, &p.DisplayName, &p.Role, &p.Active, &p.Notes, &expiry, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if expiry.Valid {
		p.SubscriptionExpiresAt = &expiry.Time
	}
	return &p, nil
}

// ListSubscribers returns identities in administrative order.
func (s *Store) ListSubscribers() ([]SubscriberProfile, error) {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, "SELECT "+profileColumns+" FROM users ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := []SubscriberProfile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetProfileByID returns one local identity.
func (s *Store) GetProfileByID(id string) (*SubscriberProfile, error) {
	ctx, cancel := dbContext()
	defer cancel()
	return scanProfile(s.DB.QueryRowContext(ctx, "SELECT "+profileColumns+" FROM users WHERE id=$1", id))
}

// CreateSubscriber creates a pending account; roles are never accepted from the browser.
func (s *Store) CreateSubscriber(req CreateSubscriberRequest) (*SubscriberProfile, error) {
	email, err := normalizedEmail(req.Email)
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	var expiry any
	if req.SubscriptionExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.SubscriptionExpiresAt)
		if err != nil {
			return nil, err
		}
		expiry = t
	}
	ctx, cancel := dbContext()
	defer cancel()
	id := RandomToken()
	p, err := scanProfile(s.DB.QueryRowContext(ctx, "INSERT INTO users(id,email,password_hash,display_name,notes,subscription_expires_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(email) DO NOTHING RETURNING "+profileColumns, id, email, hash, req.DisplayName, req.Notes, expiry))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDuplicateSignup
	}
	return p, err
}

// SignUpPending leaves public signups awaiting administrative approval.
func (s *Store) SignUpPending(req SignUpRequest) (*SubscriberProfile, error) {
	return s.CreateSubscriber(CreateSubscriberRequest{Email: req.Email, Password: req.Password, DisplayName: req.DisplayName, Notes: req.Notes})
}

// UpdateSubscriber updates local account state and revokes sessions on credential/state changes.
func (s *Store) UpdateSubscriber(req UpdateSubscriberRequest) (*SubscriberProfile, error) {
	p, err := s.GetProfileByID(req.ID)
	if err != nil {
		return nil, err
	}
	if req.DisplayName != nil {
		p.DisplayName = *req.DisplayName
	}
	if req.Notes != nil {
		p.Notes = *req.Notes
	}
	if req.Active != nil {
		p.Active = *req.Active
	}
	if req.SubscriptionExpiresAt != nil {
		if *req.SubscriptionExpiresAt == "" {
			p.SubscriptionExpiresAt = nil
		} else {
			t, err := time.Parse(time.RFC3339, *req.SubscriptionExpiresAt)
			if err != nil {
				return nil, err
			}
			p.SubscriptionExpiresAt = &t
		}
	}
	var hash any
	if req.Password != nil {
		hash, err = HashPassword(*req.Password)
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if _, err = tx.ExecContext(ctx, `UPDATE users SET display_name=$2,notes=$3,active=$4,subscription_expires_at=$5,password_hash=COALESCE($6,password_hash),updated_at=now() WHERE id=$1`, p.ID, p.DisplayName, p.Notes, p.Active, p.SubscriptionExpiresAt, hash); err != nil {
		return nil, err
	}
	if req.Password != nil || req.Active != nil || req.SubscriptionExpiresAt != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE user_id=$1`, p.ID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetProfileByID(req.ID)
}

// DeleteSubscriber disables an identity without destroying organization history.
func (s *Store) DeleteSubscriber(id string) error {
	v := false
	_, err := s.UpdateSubscriber(UpdateSubscriberRequest{ID: id, Active: &v})
	return err
}

// GenerateRecoveryLink creates a single-use local recovery token.
func (s *Store) GenerateRecoveryLink(email, redirectTo string) (string, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email=lower(trim($1))`, email).Scan(&id); err != nil {
		return "", err
	}
	return s.GenerateActionLink(id, "recovery", redirectTo, time.Hour)
}

// GenerateActionLink invalidates previous tokens of the same purpose.
func (s *Store) GenerateActionLink(id, purpose, redirectTo string, ttl time.Duration) (string, error) {
	if purpose != "recovery" && purpose != "activation" {
		return "", fmt.Errorf("invalid token purpose")
	}
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer rollback(tx)
	if _, err = tx.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, id); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_action_tokens SET consumed_at=now() WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL`, id, purpose); err != nil {
		return "", err
	}
	token := RandomToken()
	if _, err = tx.ExecContext(ctx, `INSERT INTO auth_action_tokens(token_hash,user_id,purpose,expires_at) VALUES($1,$2,$3,$4)`, TokenHash(token), id, purpose, time.Now().Add(ttl)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return strings.TrimRight(redirectTo, "/") + "#token=" + token + "&type=" + purpose, nil
}

// WasOrganizationRevoked prevents automatic organization recreation.
func (s *Store) WasOrganizationRevoked(id string) (bool, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var revoked bool
	err := s.DB.QueryRowContext(ctx, `SELECT organization_revoked FROM users WHERE id=$1`, id).Scan(&revoked)
	return revoked, err
}
