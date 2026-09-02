package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// OpenPostgres connects to a PostgreSQL database (Supabase) using DATABASE_URL.
func OpenPostgres(dsn string) (*DB, error) {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	conn.SetMaxOpenConns(maxConns())
	conn.SetMaxIdleConns(maxConns())
	// Recicla conexões antigas: provedores em nuvem (Supabase/PgBouncer)
	// derrubam conexões ociosas; pool com conexões mortas causa erros
	// intermitentes ("connection closed") sob carga alta.
	conn.SetConnMaxLifetime(30 * time.Minute)
	conn.SetConnMaxIdleTime(5 * time.Minute)

	db := &DB{conn: conn}
	if err := db.migratePostgres(); err != nil {
		// Cleanup on the failure path: a close error is not actionable here.
		_ = conn.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return db, nil
}

// maxConns retorna o tamanho do pool de conexões (env DB_MAX_CONNS, padrão 20).
func maxConns() int {
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 20
}

// OpenFromEnv opens PostgreSQL; DATABASE_URL is required.
func OpenFromEnv() (*DB, error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL não definido (o backend exige PostgreSQL/Supabase)")
	}
	return OpenPostgres(dsn)
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

// safeTestDBName reports whether name can be interpolated into DDL safely.
func safeTestDBName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, r := range name {
		if !isTestDBNameChar(r) {
			return false
		}
	}
	return true
}

func isTestDBNameChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
}

// dsnForDatabase derives a URL-style DSN pointing at the given database name.
func dsnForDatabase(baseDSN, name string) (string, error) {
	u, err := url.Parse(baseDSN)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("DSN deve ser uma URL postgres:// (recebido: %q)", baseDSN)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// CreateTestDatabase creates a disposable database named uniquely after the
// server reachable via baseDSN and returns its DSN plus a cleanup function
// that drops it. Intended for tests: never point it at a production server.
func CreateTestDatabase(baseDSN string) (string, func(), error) {
	baseDSN = strings.TrimSpace(baseDSN)
	if baseDSN == "" {
		return "", nil, fmt.Errorf("base DSN vazio")
	}
	name := fmt.Sprintf("tlm_test_%d", time.Now().UnixNano())
	if !safeTestDBName(name) {
		return "", nil, fmt.Errorf("nome de banco inválido: %s", name)
	}
	dsn, err := dsnForDatabase(baseDSN, name)
	if err != nil {
		return "", nil, err
	}

	conn, err := sql.Open("pgx", baseDSN)
	if err != nil {
		return "", nil, fmt.Errorf("connect admin: %w", err)
	}
	if _, err := conn.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, name)); err != nil {
		_ = conn.Close()
		return "", nil, fmt.Errorf("create database: %w", err)
	}

	cleanup := func() {
		_, _ = conn.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, name))
		_ = conn.Close()
	}
	return dsn, cleanup, nil
}
