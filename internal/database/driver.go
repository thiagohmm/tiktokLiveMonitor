package database

import (
	"database/sql"
	"fmt"
	"strings"
)

// rebindQuery converts '?' placeholders into PostgreSQL '$n' parameters.
func rebindQuery(query string) string {
	var b strings.Builder
	n := 1
	for _, r := range query {
		if r == '?' {
			b.WriteString(fmt.Sprintf("$%d", n))
			n++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// bind adapts a query written with '?' placeholders for PostgreSQL, quoting
// the camel-case "uniqueId" column name.
func (db *DB) bind(query string) string {
	q := rebindQuery(query)
	return strings.ReplaceAll(q, "uniqueId", `"uniqueId"`)
}

func (db *DB) exec(query string, args ...any) (sql.Result, error) {
	return db.conn.Exec(db.bind(query), args...)
}

func (db *DB) query(query string, args ...any) (*sql.Rows, error) {
	return db.conn.Query(db.bind(query), args...)
}

func (db *DB) queryRow(query string, args ...any) *sql.Row {
	return db.conn.QueryRow(db.bind(query), args...)
}

// insertID runs an INSERT and returns the generated id via RETURNING.
func (db *DB) insertID(query string, args ...any) (int64, error) {
	var id int64
	if err := db.queryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// upsertRoomLikeTotal keeps the highest like total of a live.
func (db *DB) upsertRoomLikeTotal(liveName string, total int64) error {
	_, err := db.exec(
		`INSERT INTO room_like_totals (live_name, total, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT (live_name) DO UPDATE SET
			total = GREATEST(room_like_totals.total, EXCLUDED.total),
			updated_at = CURRENT_TIMESTAMP`,
		liveName, total,
	)
	return err
}
