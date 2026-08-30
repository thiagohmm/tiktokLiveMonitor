package database

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// OpenPostgres connects to a PostgreSQL database (Supabase) using DATABASE_URL.
func OpenPostgres(dsn string) (*DB, error) {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	conn.SetMaxOpenConns(20)

	db := &DB{conn: conn, driver: driverPostgres}
	if err := db.migratePostgres(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return db, nil
}

// OpenFromEnv opens PostgreSQL when DATABASE_URL is set, otherwise SQLite in DB_DIR.
func OpenFromEnv(baseDir string) (*DB, error) {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_URL")); dsn != "" {
		return OpenPostgres(dsn)
	}
	return Open(baseDir)
}

func (db *DB) migratePostgres() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS false_positives (
			id BIGSERIAL PRIMARY KEY,
			comment TEXT NOT NULL,
			category TEXT NOT NULL,
			expected TEXT DEFAULT 'NAO',
			timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS anomaly_logs (
			id BIGSERIAL PRIMARY KEY,
			live_name TEXT NOT NULL,
			day DATE NOT NULL,
			timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			"uniqueId" TEXT,
			comment TEXT NOT NULL,
			is_anomaly BOOLEAN,
			category TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS gifts (
			id BIGSERIAL PRIMARY KEY,
			live_name TEXT NOT NULL,
			"uniqueId" TEXT NOT NULL,
			nickname TEXT NOT NULL,
			gift_name TEXT NOT NULL,
			repeat_count INTEGER DEFAULT 1,
			gift_type INTEGER DEFAULT 0,
			timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS shares (
			id BIGSERIAL PRIMARY KEY,
			live_name TEXT NOT NULL,
			"uniqueId" TEXT NOT NULL,
			nickname TEXT NOT NULL,
			timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS likes (
			id BIGSERIAL PRIMARY KEY,
			live_name TEXT NOT NULL,
			"uniqueId" TEXT NOT NULL,
			nickname TEXT NOT NULL,
			like_count INTEGER DEFAULT 1,
			timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS room_like_totals (
			live_name TEXT PRIMARY KEY,
			total BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS user_messages (
			id BIGSERIAL PRIMARY KEY,
			live_name TEXT NOT NULL DEFAULT '',
			"uniqueId" TEXT NOT NULL,
			username TEXT NOT NULL,
			message TEXT NOT NULL,
			timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS target_gift_history (
			id BIGSERIAL PRIMARY KEY,
			live_name TEXT NOT NULL,
			"uniqueId" TEXT NOT NULL,
			nickname TEXT NOT NULL,
			gift_name TEXT NOT NULL,
			received_at TIMESTAMPTZ NOT NULL,
			answered_at TIMESTAMPTZ,
			response_type TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS gift_goals (
			id BIGSERIAL PRIMARY KEY,
			live_name TEXT NOT NULL,
			title TEXT NOT NULL,
			gift_name TEXT NOT NULL DEFAULT '',
			target_units INTEGER NOT NULL,
			status TEXT NOT NULL,
			milestones TEXT DEFAULT '[]',
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS pinned_comments (
			id BIGSERIAL PRIMARY KEY,
			live_name TEXT NOT NULL,
			"uniqueId" TEXT NOT NULL,
			nickname TEXT NOT NULL,
			comment TEXT NOT NULL,
			pin_id TEXT,
			is_follower INTEGER,
			timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_pinned_comments_pin
			ON pinned_comments(live_name, pin_id)
			WHERE pin_id IS NOT NULL AND pin_id != ''`,
		`CREATE INDEX IF NOT EXISTS idx_user_messages_dedup
			ON user_messages(LOWER("uniqueId"), LOWER(message))`,
	}

	for _, s := range stmts {
		if _, err := db.conn.Exec(s); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}
	return nil
}
