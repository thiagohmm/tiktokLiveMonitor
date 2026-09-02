package database

import (
	"database/sql"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"sync"
)

// DB wraps the database connection with thread-safe access and implements model.Repository.
type DB struct {
	conn *sql.DB
	mu   sync.Mutex
}

var _ model.Repository = (*DB)(nil)

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// closeRows closes rows; the read error is already surfaced via rows.Err(),
// so a close error here is not actionable and is intentionally ignored
// (keeps the defer idiom readable across repository methods).
func closeRows(rows *sql.Rows) {
	_ = rows.Close()
}
