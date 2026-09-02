// Package database provides the PostgreSQL (Supabase) implementation of the model.Repository interface.
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

// LogAnomaly records a moderation decision.
func (db *DB) LogAnomaly(liveName, comment string, isAnomaly bool, category, uniqueID string) error {
	now := time.Now()
	day := now.UTC().Format("2006-01-02")

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.exec(
		`INSERT INTO anomaly_logs (live_name, day, uniqueId, comment, is_anomaly, category)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		liveName, day, uniqueID, comment, isAnomaly, category,
	)
	if err != nil {
		return fmt.Errorf("insert anomaly: %w", err)
	}
	return nil
}

// GetRecentModerations returns the latest N moderation records.
func (db *DB) GetRecentModerations(limit int) ([]model.AnomalyLog, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs ORDER BY timestamp DESC LIMIT ?`,
		limit,
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
func (db *DB) GetRecentAnomalyLogs(limit int) ([]model.AnomalyLog, error) {
	return db.GetRecentModerations(limit)
}

// GetAnomalyLogsByLiveName retrieves logs for a specific live name.
func (db *DB) GetAnomalyLogsByLiveName(liveName string) ([]model.AnomalyLog, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		"SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category FROM anomaly_logs WHERE live_name = ?",
		liveName,
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
func (db *DB) GetAnomalyLogsByUser(uniqueID string, limit int) ([]model.AnomalyLog, error) {
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
		 WHERE LOWER(uniqueId) = LOWER(?) AND is_anomaly = TRUE
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		uniqueID, limit,
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

// ClearHistory removes all anomaly logs.
func (db *DB) ClearHistory() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.exec("DELETE FROM anomaly_logs")
	if err != nil {
		return 0, fmt.Errorf("clear history: %w", err)
	}
	return result.RowsAffected()
}

// DeleteModeration removes a single anomaly log by ID.
func (db *DB) DeleteModeration(id int64) (int64, error) {
	if id <= 0 {
		return 0, model.ErrInvalidID
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.exec("DELETE FROM anomaly_logs WHERE id = ?", id)
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

// GetTodayAnomalyLogs returns anomaly logs from today for the given live name.
func (db *DB) GetTodayAnomalyLogs(liveName string) ([]model.AnomalyLog, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs
		 WHERE day = date('now') AND live_name = ?
		 ORDER BY timestamp ASC`,
		liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query today anomaly logs: %w", err)
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
