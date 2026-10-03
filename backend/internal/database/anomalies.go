// Package database provides the PostgreSQL implementation of the model.Repository interface.
package database

import (
	"database/sql"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"strings"
	"time"
)

// GetFalsePositiveComments returns distinct comments marked as false positives
// (expected = 'NAO'), newest first.
func (db *DB) GetFalsePositiveComments(limit int) ([]string, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT comment, MAX(timestamp) AS latest FROM false_positives
		 WHERE expected = 'NAO' GROUP BY comment ORDER BY latest DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query false positive comments: %w", err)
	}
	defer closeRows(rows)

	out := make([]string, 0)
	for rows.Next() {
		var c string
		var latest sql.NullString
		if err := rows.Scan(&c, &latest); err != nil {
			return nil, fmt.Errorf("scan false positive comment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LogAnomaly records a moderation decision for one session.
func (db *DB) LogAnomaly(ref model.LiveRef, comment string, isAnomaly bool, category, uniqueID string) error {
	if !ref.Valid() {
		return model.ErrInvalidID
	}
	now := time.Now()
	day := now.UTC().Format("2006-01-02")

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.exec(
		`INSERT INTO anomaly_logs (live_id, live_name, day, uniqueId, comment, is_anomaly, category)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ref.ID, strings.TrimSpace(ref.Name), day, uniqueID, comment, isAnomaly, category,
	)
	if err != nil {
		return fmt.Errorf("insert anomaly: %w", err)
	}
	return nil
}

// GetRecentModerations returns the latest N moderation records of an organization.
func (db *DB) GetRecentModerations(orgID string, limit int) ([]model.AnomalyLog, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs WHERE `+orgSessions+` ORDER BY timestamp DESC LIMIT ?`,
		orgID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query moderations: %w", err)
	}
	defer closeRows(rows)

	var out []model.AnomalyLog
	for rows.Next() {
		a, err := scanAnomalyLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetRecentAnomalyLogs retrieves the most recent anomaly logs.
func (db *DB) GetRecentAnomalyLogs(orgID string, limit int) ([]model.AnomalyLog, error) {
	return db.GetRecentModerations(orgID, limit)
}

// GetAnomalyLogsByLiveName retrieves logs for a specific live name.
func (db *DB) GetAnomalyLogsByLiveName(orgID, liveName string) ([]model.AnomalyLog, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return nil, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		"SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category FROM anomaly_logs WHERE "+orgSessions+" AND live_name = ?",
		orgID, liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query anomaly logs by live: %w", err)
	}
	defer closeRows(rows)

	var out []model.AnomalyLog
	for rows.Next() {
		a, err := scanAnomalyLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAnomalyLogsByUser returns anomaly logs for a participant (case-insensitive).
func (db *DB) GetAnomalyLogsByUser(orgID, uniqueID string, limit int) ([]model.AnomalyLog, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return nil, err
	}
	uniqueID = strings.TrimSpace(uniqueID)
	if uniqueID == "" {
		return nil, fmt.Errorf("uniqueId is required")
	}
	if limit < 1 || limit > 500 {
		limit = 50
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs
		 WHERE `+orgSessions+` AND LOWER(uniqueId) = LOWER(?) AND is_anomaly = TRUE
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		orgID, uniqueID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query anomaly logs by user: %w", err)
	}
	defer closeRows(rows)

	var out []model.AnomalyLog
	for rows.Next() {
		a, err := scanAnomalyLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ClearHistory removes all anomaly logs of an organization.
func (db *DB) ClearHistory(orgID string) (int64, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return 0, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.exec("DELETE FROM anomaly_logs WHERE "+orgSessions, orgID)
	if err != nil {
		return 0, fmt.Errorf("clear history: %w", err)
	}
	return result.RowsAffected()
}

// DeleteModeration removes a single anomaly log of an organization by ID.
func (db *DB) DeleteModeration(orgID string, id int64) (int64, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, model.ErrInvalidID
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.exec("DELETE FROM anomaly_logs WHERE id = ? AND "+orgSessions, id, orgID)
	if err != nil {
		return 0, fmt.Errorf("delete moderation: %w", err)
	}
	return result.RowsAffected()
}

// CleanupOldAnomalies removes records older than today.
func (db *DB) CleanupOldAnomalies() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.exec("DELETE FROM anomaly_logs WHERE day < date('now')")
	if err != nil {
		return 0, fmt.Errorf("cleanup anomalies: %w", err)
	}
	return result.RowsAffected()
}

// GetSessionAnomalyLogs returns the anomaly logs of one session, in
// chronological order. Used to restore pinned users on reconnect.
func (db *DB) GetSessionAnomalyLogs(liveID string) ([]model.AnomalyLog, error) {
	liveID = strings.TrimSpace(liveID)
	if liveID == "" {
		return []model.AnomalyLog{}, nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs
		 WHERE live_id = ?
		 ORDER BY timestamp ASC`,
		liveID,
	)
	if err != nil {
		return nil, fmt.Errorf("query session anomaly logs: %w", err)
	}
	defer closeRows(rows)

	var out []model.AnomalyLog
	for rows.Next() {
		a, err := scanAnomalyLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// scanAnomalyLog reads one anomaly_logs row into a model.AnomalyLog.
func scanAnomalyLog(rows *sql.Rows) (model.AnomalyLog, error) {
	var a model.AnomalyLog
	var isAnomaly bool
	if err := rows.Scan(&a.ID, &a.LiveName, &a.Day, &a.Timestamp,
		&a.UniqueID, &a.Comment, &isAnomaly, &a.Category); err != nil {
		return model.AnomalyLog{}, fmt.Errorf("scan anomaly log: %w", err)
	}
	a.IsAnomaly = isAnomaly
	return a, nil
}
