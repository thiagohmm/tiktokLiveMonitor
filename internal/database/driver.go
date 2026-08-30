package database

import (
	"database/sql"
	"fmt"
	"strings"
)

type driverKind string

const (
	driverSQLite   driverKind = "sqlite"
	driverPostgres driverKind = "postgres"
)

func rebindQuery(driver driverKind, query string) string {
	if driver != driverPostgres {
		return query
	}
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

func (db *DB) bind(query string) string {
	q := rebindQuery(db.driver, query)
	if db.driver == driverPostgres {
		q = strings.ReplaceAll(q, "uniqueId", `"uniqueId"`)
	}
	return q
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

func (db *DB) insertID(query string, args ...any) (int64, error) {
	if db.driver == driverPostgres {
		var id int64
		if err := db.queryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	res, err := db.exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) upsertRoomLikeTotal(liveName string, total int64) error {
	if db.driver == driverPostgres {
		_, err := db.exec(
			`INSERT INTO room_like_totals (live_name, total, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT (live_name) DO UPDATE SET
				total = GREATEST(room_like_totals.total, EXCLUDED.total),
				updated_at = CURRENT_TIMESTAMP`,
			liveName, total,
		)
		return err
	}
	_, err := db.exec(
		`INSERT INTO room_like_totals (live_name, total, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(live_name) DO UPDATE SET
			total = MAX(total, excluded.total),
			updated_at = CURRENT_TIMESTAMP`,
		liveName, total,
	)
	return err
}
