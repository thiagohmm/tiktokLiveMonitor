package database

import (
	"context"
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
			response_type TEXT,
			is_priority BOOLEAN NOT NULL DEFAULT FALSE,
			priority_at TIMESTAMPTZ
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
		// Sessões de live (uma por conexão de monitor). O delete da administração
		// apaga por live_sessions.id — live_name é o username do streamer e não
		// identifica uma sessão. Idempotente; espelha supabase/migrations/004
		// (o REVOKE de anon/authenticated fica só lá: bancos de teste não têm
		// esses papéis).
		`CREATE TABLE IF NOT EXISTS live_sessions (
			id           TEXT PRIMARY KEY,
			live_name    TEXT NOT NULL,
			day          DATE NOT NULL,
			started_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			ended_at     TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_live_sessions_name_day
			ON live_sessions(live_name, day DESC)`,
		`ALTER TABLE live_sessions ENABLE ROW LEVEL SECURITY`,
		// live_id nas tabelas operacionais que guardam live_name.
		`ALTER TABLE user_messages        ADD COLUMN IF NOT EXISTS live_id TEXT`,
		`ALTER TABLE gifts                ADD COLUMN IF NOT EXISTS live_id TEXT`,
		`ALTER TABLE shares               ADD COLUMN IF NOT EXISTS live_id TEXT`,
		`ALTER TABLE likes                ADD COLUMN IF NOT EXISTS live_id TEXT`,
		`ALTER TABLE pinned_comments      ADD COLUMN IF NOT EXISTS live_id TEXT`,
		`ALTER TABLE anomaly_logs         ADD COLUMN IF NOT EXISTS live_id TEXT`,
		`ALTER TABLE target_gift_history  ADD COLUMN IF NOT EXISTS live_id TEXT`,
		`ALTER TABLE gift_goals           ADD COLUMN IF NOT EXISTS live_id TEXT`,
		`ALTER TABLE room_like_totals     ADD COLUMN IF NOT EXISTS live_id TEXT`,
		// Fila de presentes alvos: prioridade ("fura fila") + momento da
		// promoção. Idempotente; espelha supabase/migrations/003.
		`ALTER TABLE target_gift_history ADD COLUMN IF NOT EXISTS is_priority BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE target_gift_history ADD COLUMN IF NOT EXISTS priority_at TIMESTAMPTZ`,
		// RLS (default deny) nas tabelas operacionais: o frontend nunca as
		// consulta diretamente (todo dado passa pela API Go) e o backend
		// conecta como superusuário/BYPASSRLS (pooler Supabase = postgres),
		// que ignora RLS. Idempotente; espelha supabase/migrations/002.
		`ALTER TABLE false_positives      ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE anomaly_logs         ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE gifts                ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE shares               ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE likes                ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE room_like_totals     ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE user_messages        ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE target_gift_history  ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE gift_goals           ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE pinned_comments      ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE settings             ENABLE ROW LEVEL SECURITY`,
	}

	for _, s := range stmts {
		if _, err := db.conn.Exec(s); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}
	return db.migrateLiveSessions()
}

// liveMigrationBatchSize bounds how many rows each backfill statement rewrites,
// so the migration never holds one giant transaction over a big production table.
const liveMigrationBatchSize = 5000

// liveMigrationLockTimeout makes a migration statement that cannot take its lock
// fail fast instead of queueing every write behind it. Railway allows 300s for
// the healthcheck, and every step here is idempotent, so failing loudly and
// retrying on the next boot beats hanging.
const liveMigrationLockTimeout = "30s"

// legacyLiveIDExpression is the deterministic id of the synthetic session that
// owns rows written before live_sessions existed. It must be evaluated the same
// way in the backfill INSERT and in every UPDATE, otherwise a row ends up
// pointing at a session that does not exist.
func legacyLiveIDExpression(dayExpr string) string {
	return "'legacy:' || md5(live_name || ':' || (" + dayExpr + ")::text)"
}

// liveSource describes how to derive the day and the instant of one table. Each
// table names its timestamp column differently.
type liveSource struct {
	table   string
	dayExpr string
	tsExpr  string
}

// liveIDColumns lists every table that carries live_id. DeleteLiveSession covers
// exactly this set (asserted against information_schema by a test).
var liveIDColumns = []liveSource{
	{"user_messages", "(timestamp AT TIME ZONE 'UTC')::date", "timestamp"},
	{"gifts", "(timestamp AT TIME ZONE 'UTC')::date", "timestamp"},
	{"shares", "(timestamp AT TIME ZONE 'UTC')::date", "timestamp"},
	{"likes", "(timestamp AT TIME ZONE 'UTC')::date", "timestamp"},
	{"pinned_comments", "(timestamp AT TIME ZONE 'UTC')::date", "timestamp"},
	{"anomaly_logs", "day", "timestamp"},
	{"target_gift_history", "(received_at AT TIME ZONE 'UTC')::date", "received_at"},
	{"gift_goals", "(COALESCE(created_at, CURRENT_TIMESTAMP) AT TIME ZONE 'UTC')::date", "COALESCE(created_at, CURRENT_TIMESTAMP)"},
	{"room_like_totals", "(COALESCE(updated_at, CURRENT_TIMESTAMP) AT TIME ZONE 'UTC')::date", "COALESCE(updated_at, CURRENT_TIMESTAMP)"},
}

// migrateLiveSessions runs the non-additive part of the live_sessions migration,
// in the only order that is safe: concurrent indexes -> backfill -> NOT NULL ->
// primary key. It is written to run against a LIVE production database:
//
//   - indexes are built with CREATE INDEX CONCURRENTLY, so the scan does not
//     block writes;
//   - the backfill rewrites rows in batches (one transaction each) instead of a
//     single statement over every row of every table;
//   - SET NOT NULL goes through a validated CHECK constraint, avoiding the
//     full-table scan under ACCESS EXCLUSIVE that ALTER COLUMN would need;
//   - the room_like_totals primary key is attached to a concurrently built
//     unique index instead of being rebuilt under an exclusive lock;
//   - lock_timeout makes any step that cannot take its lock fail fast instead of
//     blocking the application behind it.
//
// Every step checks what is already done, so a container restart in the middle
// of the migration resumes instead of failing. Called with db.mu held by
// migratePostgres.
func (db *DB) migrateLiveSessions() error {
	ctx := context.Background()
	conn, err := db.conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	// A dedicated connection is required: the SETs below are session-scoped, and
	// the pool would otherwise hand out another connection for the next statement.
	defer func() { _ = conn.Close() }()

	exec := func(query string) error {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("%s: %w", migrationLabel(query), err)
		}
		return nil
	}

	if err := exec(`SET lock_timeout = '` + liveMigrationLockTimeout + `'`); err != nil {
		return err
	}
	if err := exec("SET statement_timeout = 0"); err != nil {
		return err
	}

	// 0) A container killed in the middle of a CREATE INDEX CONCURRENTLY leaves an
	//    index marked invalid, which IF NOT EXISTS would then skip forever.
	if err := dropInvalidIndexes(ctx, conn); err != nil {
		return err
	}

	// 1) live_id indexes (concurrent: no write blocking).
	for _, s := range liveIDColumns {
		if s.table == "room_like_totals" {
			// The primary key built in step 4 already provides this index.
			continue
		}
		if err := exec(fmt.Sprintf(
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_%s_live_id ON %s(live_id)", s.table, s.table)); err != nil {
			return err
		}
	}
	// Message dedup and pinned comments become session-scoped. The rebuild only
	// happens while the index still describes the old columns: running DROP +
	// CREATE on every boot would rebuild (and re-scan) the message index on every
	// deploy, eating into the healthcheck window for nothing.
	rebuilds := []struct {
		name     string
		required string
		create   string
	}{
		{
			name:     "idx_user_messages_dedup",
			required: "live_id",
			create: `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_user_messages_dedup
				ON user_messages(live_id, LOWER("uniqueId"), LOWER(message))`,
		},
		{
			name:     "idx_pinned_comments_pin",
			required: "live_id",
			create: `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_pinned_comments_pin
				ON pinned_comments(live_id, pin_id)
				WHERE pin_id IS NOT NULL AND pin_id != ''`,
		},
	}
	for _, r := range rebuilds {
		stale, err := indexLacksColumn(ctx, conn, r.name, r.required)
		if err != nil {
			return err
		}
		if !stale {
			continue
		}
		if err := exec(fmt.Sprintf("DROP INDEX CONCURRENTLY IF EXISTS %s", r.name)); err != nil {
			return err
		}
		if err := exec(r.create); err != nil {
			return err
		}
	}

	// 2) Synthetic sessions for everything that already existed, one per
	//    (live_name, day). Per table rather than one huge UNION, so each statement
	//    stays small. COALESCE on the instant too: a legacy row with a NULL
	//    timestamp would make MIN/MAX NULL and break the NOT NULL session columns.
	for _, s := range liveIDColumns {
		if err := exec(fmt.Sprintf(`
			INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at)
			SELECT %s, live_name, %s AS day,
			       MIN(COALESCE(%s, CURRENT_TIMESTAMP)), MAX(COALESCE(%s, CURRENT_TIMESTAMP))
			FROM %s
			WHERE live_id IS NULL
			GROUP BY live_name, %s
			ON CONFLICT (id) DO NOTHING`,
			legacyLiveIDExpression(s.dayExpr), s.dayExpr, s.tsExpr, s.tsExpr, s.table, s.dayExpr)); err != nil {
			return err
		}
	}

	// 3) Stamp the old rows with the id of their synthetic session, in batches.
	for _, s := range liveIDColumns {
		if err := backfillLiveID(ctx, conn, s); err != nil {
			return err
		}
	}

	// 4) With no NULLs left, lock the column down.
	for _, s := range liveIDColumns {
		if err := setLiveIDNotNull(ctx, conn, s.table); err != nil {
			return err
		}
	}

	// 5) room_like_totals becomes session-scoped.
	return ensureRoomLikeTotalsSessionPK(ctx, conn)
}

// backfillLiveID stamps live_id on the rows that predate the migration. The
// table is rewritten in small transactions so a big table cannot hold one long
// lock; room_like_totals is tiny (one row per session) and needs no batching.
func backfillLiveID(ctx context.Context, conn *sql.Conn, s liveSource) error {
	expr := legacyLiveIDExpression(s.dayExpr)

	if s.table == "room_like_totals" {
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(
			"UPDATE room_like_totals SET live_id = %s WHERE live_id IS NULL", expr)); err != nil {
			return fmt.Errorf("backfill room_like_totals.live_id: %w", err)
		}
		return nil
	}

	for {
		res, err := conn.ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s SET live_id = %s
			 WHERE id IN (SELECT id FROM %s WHERE live_id IS NULL LIMIT %d)`,
			s.table, expr, s.table, liveMigrationBatchSize))
		if err != nil {
			return fmt.Errorf("backfill %s.live_id: %w", s.table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("backfill %s.live_id rows affected: %w", s.table, err)
		}
		if n == 0 {
			return nil
		}
	}
}

// setLiveIDNotNull marks live_id as NOT NULL without the full table scan that
// ALTER COLUMN SET NOT NULL would take under ACCESS EXCLUSIVE: a CHECK constraint
// is added NOT VALID, validated (SHARE UPDATE EXCLUSIVE, writes keep flowing) and
// only then used to set the column. Constraint names are derived from the table,
// so a leftover from an interrupted run is dropped first.
func setLiveIDNotNull(ctx context.Context, conn *sql.Conn, table string) error {
	var nullable string
	if err := conn.QueryRowContext(ctx,
		`SELECT is_nullable FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1 AND column_name = 'live_id'`,
		table).Scan(&nullable); err != nil {
		return fmt.Errorf("read %s.live_id nullability: %w", table, err)
	}
	if nullable == "NO" {
		return nil
	}

	constraint := table + "_live_id_not_null"
	for _, stmt := range []string{
		fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s", table, constraint),
		fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (live_id IS NOT NULL) NOT VALID", table, constraint),
		fmt.Sprintf("ALTER TABLE %s VALIDATE CONSTRAINT %s", table, constraint),
		// With the constraint validated, Postgres skips the scan here.
		fmt.Sprintf("ALTER TABLE %s ALTER COLUMN live_id SET NOT NULL", table),
		fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s", table, constraint),
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("set %s.live_id NOT NULL: %w", table, err)
		}
	}
	return nil
}

// ensureRoomLikeTotalsSessionPK moves the primary key of room_like_totals from
// live_name to live_id. A unique index is built concurrently and attached with
// ADD CONSTRAINT ... USING INDEX, so the table is not rewritten under an
// exclusive lock. Reads are unaffected: LikeTotals still takes MAX(total) by
// live_name across the streamer's sessions.
func ensureRoomLikeTotalsSessionPK(ctx context.Context, conn *sql.Conn) error {
	var onLiveID int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
		WHERE tc.table_schema = 'public' AND tc.table_name = 'room_like_totals'
		  AND tc.constraint_type = 'PRIMARY KEY' AND kcu.column_name = 'live_id'`).Scan(&onLiveID); err != nil {
		return fmt.Errorf("read room_like_totals primary key: %w", err)
	}
	if onLiveID > 0 {
		return nil
	}

	for _, stmt := range []string{
		`CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS room_like_totals_live_id_key
			ON room_like_totals(live_id)`,
		`ALTER TABLE room_like_totals DROP CONSTRAINT IF EXISTS room_like_totals_pkey`,
		`ALTER TABLE room_like_totals ADD CONSTRAINT room_like_totals_pkey
			PRIMARY KEY USING INDEX room_like_totals_live_id_key`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("room_like_totals primary key: %w", err)
		}
	}
	return nil
}

// liveIDTableNames lists the tables that carry live_id, from the single source
// of truth (liveIDColumns). DeleteLiveSession deletes from exactly these tables
// and a test asserts this list against information_schema, so a new table with
// live_id cannot be silently left out of the delete.
func liveIDTableNames() []string {
	names := make([]string, 0, len(liveIDColumns))
	for _, s := range liveIDColumns {
		names = append(names, s.table)
	}
	return names
}

// indexLacksColumn reports whether the named index exists but does not include
// column. A missing index also counts as "needs (re)building".
func indexLacksColumn(ctx context.Context, conn *sql.Conn, name, column string) (bool, error) {
	var def string
	err := conn.QueryRowContext(ctx,
		`SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname = $1 AND relkind = 'i'`, name).Scan(&def)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read index %s definition: %w", name, err)
	}
	return !strings.Contains(def, column), nil
}

// dropInvalidIndexes removes indexes left invalid by an interrupted
// CREATE INDEX CONCURRENTLY, so the retry can rebuild them.
func dropInvalidIndexes(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT c.relname FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND NOT i.indisvalid`)
	if err != nil {
		return fmt.Errorf("query invalid indexes: %w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan invalid index: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("invalid indexes rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close invalid indexes: %w", err)
	}
	for _, name := range names {
		if _, err := conn.ExecContext(ctx,
			fmt.Sprintf("DROP INDEX CONCURRENTLY IF EXISTS %s", quoteIdent(name))); err != nil {
			return fmt.Errorf("drop invalid index %s: %w", name, err)
		}
	}
	return nil
}

// quoteIdent quotes an identifier read from the catalog.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// migrationLabel shortens a statement for error messages.
func migrationLabel(query string) string {
	q := strings.TrimSpace(query)
	if i := strings.IndexAny(q, "\r\n"); i >= 0 {
		q = q[:i]
	}
	if len(q) > 90 {
		q = q[:90]
	}
	return q
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
