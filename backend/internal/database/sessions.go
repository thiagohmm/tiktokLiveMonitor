package database

import (
	"database/sql"
	"fmt"
	"time"
)

func parseStoredTime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	// Timestamps are stored in UTC (CURRENT_TIMESTAMP and writers format UTC),
	// so parse naive values as UTC to keep day comparisons consistent.
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}

func (db *DB) maxTimestamp(query string, args ...any) (time.Time, bool, error) {
	var raw sql.NullString
	err := db.queryRow(query, args...).Scan(&raw)
	if err != nil {
		return time.Time{}, false, err
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, false, nil
	}
	ts, err := parseStoredTime(raw.String)
	if err != nil {
		return time.Time{}, false, err
	}
	return ts, true, nil
}

// GetLastSessionActivity returns the latest timestamp among gifts and
// anomaly logs for liveName, plus user_messages/pinned/target gifts of the
// same live.
func (db *DB) GetLastSessionActivity(liveName string) (time.Time, bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	queries := []struct {
		q    string
		args []any
	}{
		{"SELECT MAX(timestamp) FROM gifts WHERE live_name = ?", []any{liveName}},
		{"SELECT MAX(timestamp) FROM anomaly_logs WHERE live_name = ?", []any{liveName}},
		{"SELECT MAX(timestamp) FROM user_messages WHERE live_name = ?", []any{liveName}},
		{"SELECT MAX(timestamp) FROM pinned_comments WHERE live_name = ?", []any{liveName}},
		{"SELECT MAX(received_at) FROM target_gift_history WHERE live_name = ?", []any{liveName}},
	}

	var latest time.Time
	found := false
	for _, q := range queries {
		ts, ok, err := db.maxTimestamp(q.q, q.args...)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("last session activity: %w", err)
		}
		if !ok {
			continue
		}
		if !found || ts.After(latest) {
			latest = ts
			found = true
		}
	}
	return latest, found, nil
}

// DeleteSessionData removes gifts, anomaly logs, user_messages, pinned
// comments and target gift history for liveName (only rows of that live).
func (db *DB) DeleteSessionData(liveName string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	// Rollback is a no-op once the transaction commits; its error is not
	// actionable, so it is intentionally ignored.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(db.bind("DELETE FROM gifts WHERE live_name = ?"), liveName); err != nil {
		return fmt.Errorf("delete session gifts: %w", err)
	}
	if _, err := tx.Exec(db.bind("DELETE FROM anomaly_logs WHERE live_name = ?"), liveName); err != nil {
		return fmt.Errorf("delete session anomalies: %w", err)
	}
	if _, err := tx.Exec(db.bind("DELETE FROM user_messages WHERE live_name = ?"), liveName); err != nil {
		return fmt.Errorf("delete session messages: %w", err)
	}
	if _, err := tx.Exec(db.bind("DELETE FROM pinned_comments WHERE live_name = ?"), liveName); err != nil {
		return fmt.Errorf("delete session pinned comments: %w", err)
	}
	if _, err := tx.Exec(db.bind("DELETE FROM target_gift_history WHERE live_name = ?"), liveName); err != nil {
		return fmt.Errorf("delete session target gift history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	return nil
}

// ExecSQL runs a raw statement. Used by tests to seed timestamps.
func (db *DB) ExecSQL(query string, args ...any) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.exec(query, args...)
	return err
}
