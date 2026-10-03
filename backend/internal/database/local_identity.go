package database

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// SQLDB exposes the shared pool to local identity and team services.
func (db *DB) SQLDB() *sql.DB { return db.conn }

func (db *DB) migrateLocalIdentity() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
		 id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE CHECK(email=lower(trim(email))), display_name TEXT NOT NULL DEFAULT '',
		 role TEXT NOT NULL DEFAULT 'subscriber' CHECK(role IN ('admin','subscriber')), active BOOLEAN NOT NULL DEFAULT FALSE,
		 password_hash TEXT, notes TEXT NOT NULL DEFAULT '', subscription_expires_at TIMESTAMPTZ,
		 organization_revoked BOOLEAN NOT NULL DEFAULT FALSE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS auth_sessions(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		 csrf_hash TEXT NOT NULL,expires_at TIMESTAMPTZ NOT NULL,revoked_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(user_id)`,
		`CREATE TABLE IF NOT EXISTS auth_action_tokens(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		 purpose TEXT NOT NULL CHECK(purpose IN ('activation','recovery')),expires_at TIMESTAMPTZ NOT NULL,consumed_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS auth_rate_limits(key TEXT PRIMARY KEY,failures INTEGER NOT NULL DEFAULT 0,locked_until TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`ALTER TABLE organizations ADD COLUMN IF NOT EXISTS primary_owner_user_id TEXT`,
		`ALTER TABLE organizations ADD COLUMN IF NOT EXISTS team_rules_enabled BOOLEAN NOT NULL DEFAULT FALSE`,
		`CREATE TABLE IF NOT EXISTS organization_seat_subscriptions(id TEXT PRIMARY KEY,org_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		 quantity INTEGER NOT NULL DEFAULT 0 CHECK(quantity>=0),starts_at TIMESTAMPTZ NOT NULL DEFAULT now(),expires_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS organization_seats(org_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		 number INTEGER NOT NULL CHECK(number>=1),user_id TEXT UNIQUE,PRIMARY KEY(org_id,number))`,
		`CREATE TABLE IF NOT EXISTS organization_invitations(id TEXT PRIMARY KEY,org_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		 email TEXT NOT NULL,seat_number INTEGER NOT NULL,token_hash TEXT NOT NULL UNIQUE,expires_at TIMESTAMPTZ NOT NULL,
		 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','cancelled','expired')),
		 delivery_status TEXT NOT NULL DEFAULT 'pending' CHECK(delivery_status IN ('pending','sent','failed')),invited_by TEXT NOT NULL,
		 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),FOREIGN KEY(org_id,seat_number) REFERENCES organization_seats(org_id,number))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_pending_invitation_email ON organization_invitations(org_id,email) WHERE status='pending'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_reserved_seat ON organization_invitations(org_id,seat_number) WHERE status='pending'`,
		`CREATE TABLE IF NOT EXISTS organization_seat_payments(id TEXT PRIMARY KEY,org_id TEXT NOT NULL REFERENCES organizations(id),reference TEXT NOT NULL UNIQUE,
		 quantity INTEGER NOT NULL CHECK(quantity>=0),price_cents BIGINT NOT NULL CHECK(price_cents>=0),amount_cents BIGINT NOT NULL CHECK(amount_cents>=0),
		 starts_at TIMESTAMPTZ NOT NULL,expires_at TIMESTAMPTZ NOT NULL CHECK(expires_at>starts_at),kind TEXT NOT NULL CHECK(kind IN ('payment','temporary')),
		 notes TEXT NOT NULL DEFAULT '',created_by TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS organization_allowed_lives(org_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,username TEXT NOT NULL,
		 active BOOLEAN NOT NULL DEFAULT TRUE,PRIMARY KEY(org_id,username))`,
		`CREATE TABLE IF NOT EXISTS organization_audit_logs(id BIGSERIAL PRIMARY KEY,org_id TEXT NOT NULL,actor_id TEXT NOT NULL,action TEXT NOT NULL,
		 data JSONB NOT NULL DEFAULT '{}',created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	}
	for _, statement := range statements {
		if _, err := db.conn.Exec(statement); err != nil {
			return err
		}
	}
	for _, table := range []string{"users", "auth_sessions", "auth_action_tokens", "auth_rate_limits", "organization_seats", "organization_invitations", "organization_seat_subscriptions", "organization_seat_payments", "organization_allowed_lives", "organization_audit_logs"} {
		if _, err := db.conn.Exec(`ALTER TABLE ` + table + ` ENABLE ROW LEVEL SECURITY`); err != nil {
			return err
		}
	}
	return nil
}

// CheckAllowedLive validates the normalized username in the organization's allowlist.
func (db *DB) CheckAllowedLive(ctx context.Context, org, name string) error {
	if org == model.DefaultOrgID {
		return nil
	}
	var allowed bool
	err := db.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organizations o WHERE o.id=$1 AND (NOT o.team_rules_enabled OR EXISTS(SELECT 1 FROM organization_allowed_lives l WHERE l.org_id=o.id AND l.username=$2 AND l.active)))`, org, name).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("conta TikTok não autorizada para esta organização")
	}
	return nil
}
