package database

import (
	"database/sql"
)

// FalsePositive represents a recorded false positive entry
type FalsePositive struct {
	ID        int
	Comment   string
	Category  string
	Expected  string
	Timestamp string
}



// GetFalsePositivesByCategory retrieves false positives for a specific category
func GetFalsePositivesByCategory(db *sql.DB, category string) ([]FalsePositive, error) {
	query := "SELECT id, comment, category, expected, timestamp FROM false_positives WHERE category = ?"
	rows, err := db.Query(query, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FalsePositive
	for rows.Next() {
		var fp FalsePositive
		if err := rows.Scan(&fp.ID, &fp.Comment, &fp.Category, &fp.Expected, &fp.Timestamp); err != nil {
			return nil, err
		}
		results = append(results, fp)
	}
	return results, nil
}

// GetRecentAnomalyLogs retrieves the most recent anomaly logs
func GetRecentAnomalyLogs(db *sql.DB, limit int) ([]AnomalyLog, error) {
	query := "SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category FROM anomaly_logs ORDER BY timestamp DESC LIMIT ?"
	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []AnomalyLog
	for rows.Next() {
		var al AnomalyLog
		if err := rows.Scan(&al.ID, &al.LiveName, &al.Day, &al.Timestamp, &al.UniqueID, &al.Comment, &al.IsAnomaly, &al.Category); err != nil {
			return nil, err
		}
		results = append(results, al)
	}
	return results, nil
}

// GetAnomalyLogsByLiveName retrieves logs for a specific live name
func GetAnomalyLogsByLiveName(db *sql.DB, liveName string) ([]AnomalyLog, error) {
	query := "SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category FROM anomaly_logs WHERE live_name = ?"
	rows, err := db.Query(query, liveName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []AnomalyLog
	for rows.Next() {
		var al AnomalyLog
		if err := rows.Scan(&al.ID, &al.LiveName, &al.Day, &al.Timestamp, &al.UniqueID, &al.Comment, &al.IsAnomaly, &al.Category); err != nil {
			return nil, err
		}
		results = append(results, al)
	}
	return results, nil
}
