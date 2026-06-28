// Package database provides SQLite persistence for moderation logs and user feedback.
package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ValidExpected values for feedback.
var ValidExpected = map[string]bool{
	"NAO":             true,
	"SIM_PERGUNTA":    true,
	"SIM_PROSELITISMO": true,
	"SIM_ODIO":        true,
	"SIM_SPAM":        true,
	"SIM_GOLPE":       true,
	"SIM_OUTRO":       true,
}

// ValidCategory values for feedback.
var ValidCategory = map[string]bool{
	"OK":           true,
	"PERGUNTA":     true,
	"PROSELITISMO": true,
	"ODIO":         true,
	"SPAM":         true,
	"GOLPE":        true,
	"OUTRO":        true,
}

// AnomalyLog represents a single moderation record.
type AnomalyLog struct {
	ID        int64  `json:"id"`
	LiveName  string `json:"live_name"`
	Day       string `json:"day"`
	Timestamp string `json:"timestamp"`
	UniqueID  string `json:"uniqueId"`
	Comment   string `json:"comment"`
	IsAnomaly bool   `json:"is_anomaly"`
	Category  string `json:"category"`
}

// Feedback represents a user-provided classification example.
type Feedback struct {
	Comment   string `json:"comment"`
	Category  string `json:"category"`
	Expected  string `json:"expected"`
}

// UserMessage represents a user message from a live stream.
type UserMessage struct {
	ID         int64     `json:"id"`
	UniqueID   string    `json:"uniqueId"`
	Username   string    `json:"username"`
	Message    string    `json:"message"`
	Timestamp  string    `json:"timestamp"`
}

// Gift represents a gift received during a live stream.
type Gift struct {
	ID         int64  `json:"id"`
	LiveName   string `json:"live_name"`
	UniqueID   string `json:"uniqueId"`
	Nickname   string `json:"nickname"`
	GiftName   string `json:"giftName"`
	RepeatCount int   `json:"repeatCount"`
	GiftType   int    `json:"giftType"`
	Timestamp  string `json:"timestamp"`
}

// DB wraps the SQLite connection with thread-safe access.
type DB struct {
	conn *sql.DB
	mu   sync.Mutex
}

// Open creates or opens the SQLite database at the given directory.
func Open(dir string) (*DB, error) {
	dbPath := filepath.Join(dir, "feedback.db")
	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	conn.SetMaxOpenConns(1) // SQLite serializes writes

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

func (db *DB) migrate() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS false_positives (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			comment TEXT NOT NULL,
			category TEXT NOT NULL,
			expected TEXT DEFAULT 'NAO',
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS anomaly_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			live_name TEXT NOT NULL,
			day DATE NOT NULL,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			uniqueId TEXT,
			comment TEXT NOT NULL,
			is_anomaly BOOLEAN,
			category TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS gifts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			live_name TEXT NOT NULL,
			uniqueId TEXT NOT NULL,
			nickname TEXT NOT NULL,
			gift_name TEXT NOT NULL,
			repeat_count INTEGER DEFAULT 1,
			gift_type INTEGER DEFAULT 0,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS user_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			uniqueId TEXT NOT NULL,
			username TEXT NOT NULL,
			message TEXT NOT NULL,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, s := range stmts {
		if _, err := db.conn.Exec(s); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}
	return nil
}

// AddFeedback stores a user-provided classification example.
func (db *DB) AddFeedback(comment, category, expected string) (int64, error) {
	comment = strings.TrimSpace(comment)
	category = strings.TrimSpace(strings.ToUpper(category))
	expected = strings.TrimSpace(strings.ToUpper(expected))

	if comment == "" {
		return 0, fmt.Errorf("comment is required")
	}
	if !ValidCategory[category] {
		return 0, fmt.Errorf("invalid category")
	}
	if !ValidExpected[expected] {
		return 0, fmt.Errorf("invalid expected")
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(
		"INSERT INTO false_positives (comment, category, expected) VALUES (?, ?, ?)",
		comment, category, expected,
	)
	if err != nil {
		return 0, fmt.Errorf("insert feedback: %w", err)
	}
	return result.LastInsertId()
}

// GetRecentFeedbacks returns the latest N feedback entries.
func (db *DB) GetRecentFeedbacks(limit int) ([]Feedback, error) {
	if limit < 1 || limit > 200 {
		limit = 10
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT comment, category, expected FROM false_positives ORDER BY timestamp DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query feedbacks: %w", err)
	}
	defer rows.Close()

	var out []Feedback
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.Comment, &f.Category, &f.Expected); err != nil {
			return nil, fmt.Errorf("scan feedback: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AddUserMessageDedup stores a user message only if it's unique for that user, keeping max 10.
func (db *DB) AddUserMessageDedup(uniqueID, username, message string) error {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	message = strings.ToLower(strings.TrimSpace(message))
	username = strings.TrimSpace(username)

	if message == "" || uniqueID == "" {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	// Check if message already exists for this user.
	var count int
	err := db.conn.QueryRow(
		"SELECT COUNT(*) FROM user_messages WHERE LOWER(uniqueId) = ? AND LOWER(message) = ?",
		uniqueID, message,
	).Scan(&count)
	if err != nil || count > 0 {
		return err
	}

	// Insert new message.
	_, err = db.conn.Exec(
		"INSERT INTO user_messages (uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?)",
		uniqueID, username, message, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert user message: %w", err)
	}

	// Keep only the 10 most recent unique messages per user.
	_, err = db.conn.Exec(`
		DELETE FROM user_messages WHERE id NOT IN (
			SELECT id FROM user_messages WHERE LOWER(uniqueId) = ?
			ORDER BY timestamp DESC LIMIT 10
		) AND LOWER(uniqueId) = ?
	`, uniqueID, uniqueID)
	if err != nil {
		return fmt.Errorf("prune user messages: %w", err)
	}
	return nil
}

// GetUserMessages returns all unique messages for a specific user.
func (db *DB) GetUserMessages(uniqueID string) ([]UserMessage, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return nil, fmt.Errorf("uniqueId is required")
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT id, uniqueId, username, message, timestamp FROM user_messages WHERE LOWER(uniqueId) = ? ORDER BY timestamp DESC",
		uniqueID,
	)
	if err != nil {
		return nil, fmt.Errorf("query user messages: %w", err)
	}
	defer rows.Close()

	var out []UserMessage
	for rows.Next() {
		var um UserMessage
		if err := rows.Scan(&um.ID, &um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
			return nil, fmt.Errorf("scan user message: %w", err)
		}
		out = append(out, um)
	}
	return out, rows.Err()
}

// GetAllUserMessages returns all user messages grouped by user.
func (db *DB) GetAllUserMessages() (map[string][]UserMessage, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT uniqueId, username, message, timestamp FROM user_messages ORDER BY uniqueId, timestamp DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("query all user messages: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]UserMessage)
	for rows.Next() {
		var um UserMessage
		if err := rows.Scan(&um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
			return nil, fmt.Errorf("scan user message: %w", err)
		}
		result[um.UniqueID] = append(result[um.UniqueID], um)
	}
	return result, rows.Err()
}

// LogAnomaly records a moderation decision.
func (db *DB) LogAnomaly(liveName, comment string, isAnomaly bool, category, uniqueID string) error {
	now := time.Now()
	day := now.Format("2006-01-02")
	var anomalyInt int
	if isAnomaly {
		anomalyInt = 1
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(
		`INSERT INTO anomaly_logs (live_name, day, uniqueId, comment, is_anomaly, category)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		liveName, day, uniqueID, comment, anomalyInt, category,
	)
	if err != nil {
		return fmt.Errorf("insert anomaly: %w", err)
	}
	return nil
}

// GetRecentModerations returns the latest N moderation records.
func (db *DB) GetRecentModerations(limit int) ([]AnomalyLog, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs ORDER BY timestamp DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query moderations: %w", err)
	}
	defer rows.Close()

	var out []AnomalyLog
	for rows.Next() {
		var a AnomalyLog
		var isAnomaly bool
		if err := rows.Scan(&a.ID, &a.LiveName, &a.Day, &a.Timestamp,
			&a.UniqueID, &a.Comment, &isAnomaly, &a.Category); err != nil {
			return nil, fmt.Errorf("scan anomaly: %w", err)
		}
		a.IsAnomaly = isAnomaly
		out = append(out, a)
	}
	return out, rows.Err()
}

// ClearHistory removes all anomaly logs.
func (db *DB) ClearHistory() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec("DELETE FROM anomaly_logs")
	if err != nil {
		return 0, fmt.Errorf("clear history: %w", err)
	}
	return result.RowsAffected()
}

// DeleteModeration removes a single anomaly log by ID.
func (db *DB) DeleteModeration(id int64) (int64, error) {
	if id <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec("DELETE FROM anomaly_logs WHERE id = ?", id)
	if err != nil {
		return 0, fmt.Errorf("delete moderation: %w", err)
	}
	return result.RowsAffected()
}

// CleanupOldAnomalies removes records older than today.
func (db *DB) CleanupOldAnomalies() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec("DELETE FROM anomaly_logs WHERE day < date('now', 'localtime')")
	if err != nil {
		return 0, fmt.Errorf("cleanup anomalies: %w", err)
	}
	return result.RowsAffected()
}

// AddGift stores a gift received during a live stream.
func (db *DB) AddGift(liveName, uniqueID, nickname, giftName string, repeatCount, giftType int) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(
		"INSERT INTO gifts (live_name, uniqueId, nickname, gift_name, repeat_count, gift_type) VALUES (?, ?, ?, ?, ?, ?)",
		liveName, uniqueID, nickname, giftName, repeatCount, giftType,
	)
	if err != nil {
		return 0, fmt.Errorf("insert gift: %w", err)
	}
	return result.LastInsertId()
}

// GetRecentGifts returns the latest N gifts.
func (db *DB) GetRecentGifts(limit int) ([]Gift, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type, timestamp FROM gifts ORDER BY timestamp DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query gifts: %w", err)
	}
	defer rows.Close()

	var out []Gift
	for rows.Next() {
		var g Gift
		if err := rows.Scan(&g.ID, &g.LiveName, &g.UniqueID, &g.Nickname, &g.GiftName, &g.RepeatCount, &g.GiftType, &g.Timestamp); err != nil {
			return nil, fmt.Errorf("scan gift: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGiftsByUser returns all gifts for a specific user.
func (db *DB) GetGiftsByUser(uniqueID string) ([]Gift, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return nil, fmt.Errorf("uniqueId is required")
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type, timestamp FROM gifts WHERE LOWER(uniqueId) = ? ORDER BY timestamp DESC",
		uniqueID,
	)
	if err != nil {
		return nil, fmt.Errorf("query gifts by user: %w", err)
	}
	defer rows.Close()

	var out []Gift
	for rows.Next() {
		var g Gift
		if err := rows.Scan(&g.ID, &g.LiveName, &g.UniqueID, &g.Nickname, &g.GiftName, &g.RepeatCount, &g.GiftType, &g.Timestamp); err != nil {
			return nil, fmt.Errorf("scan gift: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGiftSummary returns a summary of gifts grouped by user for the current live.
func (db *DB) GetGiftSummary() (map[string]map[string]int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT uniqueId, nickname, gift_name, SUM(repeat_count) as total FROM gifts GROUP BY uniqueId, nickname, gift_name ORDER BY total DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("query gift summary: %w", err)
	}
	defer rows.Close()

	summary := make(map[string]map[string]int)
	for rows.Next() {
		var uniqueID, nickname, giftName string
		var total int
		if err := rows.Scan(&uniqueID, &nickname, &giftName, &total); err != nil {
			return nil, fmt.Errorf("scan gift summary: %w", err)
		}
		if _, ok := summary[uniqueID]; !ok {
			summary[uniqueID] = make(map[string]int)
		}
		summary[uniqueID][giftName] += total
	}
	return summary, rows.Err()
}

// ClearGifts removes all gift records.
func (db *DB) ClearGifts() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec("DELETE FROM gifts")
	if err != nil {
		return 0, fmt.Errorf("clear gifts: %w", err)
	}
	return result.RowsAffected()
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}
