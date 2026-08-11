package database

import (
	"database/sql"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// GetFalsePositivesByCategory retrieves false positives for a specific category
func GetFalsePositivesByCategory(db *sql.DB, category string) ([]model.FalsePositive, error) {
	query := "SELECT id, comment, category, expected, timestamp FROM false_positives WHERE category = ?"
	rows, err := db.Query(query, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []model.FalsePositive
	for rows.Next() {
		var fp model.FalsePositive
		if err := rows.Scan(&fp.ID, &fp.Comment, &fp.Category, &fp.Expected, &fp.Timestamp); err != nil {
			return nil, err
		}
		results = append(results, fp)
	}
	return results, nil
}

// GetRecentAnomalyLogs retrieves the most recent anomaly logs
func GetRecentAnomalyLogs(db *sql.DB, limit int) ([]model.AnomalyLog, error) {
	query := "SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category FROM anomaly_logs ORDER BY timestamp DESC LIMIT ?"
	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []model.AnomalyLog
	for rows.Next() {
		var al model.AnomalyLog
		if err := rows.Scan(&al.ID, &al.LiveName, &al.Day, &al.Timestamp, &al.UniqueID, &al.Comment, &al.IsAnomaly, &al.Category); err != nil {
			return nil, err
		}
		results = append(results, al)
	}
	return results, nil
}

// GetAnomalyLogsByLiveName retrieves logs for a specific live name
func GetAnomalyLogsByLiveName(db *sql.DB, liveName string) ([]model.AnomalyLog, error) {
	query := "SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category FROM anomaly_logs WHERE live_name = ?"
	rows, err := db.Query(query, liveName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []model.AnomalyLog
	for rows.Next() {
		var al model.AnomalyLog
		if err := rows.Scan(&al.ID, &al.LiveName, &al.Day, &al.Timestamp, &al.UniqueID, &al.Comment, &al.IsAnomaly, &al.Category); err != nil {
			return nil, err
		}
		results = append(results, al)
	}
	return results, nil
}
