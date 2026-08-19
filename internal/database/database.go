// Package database provides SQLite implementation of the model.Repository interface.
package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite connection with thread-safe access and implements model.Repository.
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
		`CREATE TABLE IF NOT EXISTS target_gift_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			live_name TEXT NOT NULL,
			uniqueId TEXT NOT NULL,
			nickname TEXT NOT NULL,
			gift_name TEXT NOT NULL,
			received_at DATETIME NOT NULL,
			answered_at DATETIME,
			response_type TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS pinned_comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			live_name TEXT NOT NULL,
			uniqueId TEXT NOT NULL,
			nickname TEXT NOT NULL,
			comment TEXT NOT NULL,
			pin_id TEXT,
			is_follower INTEGER,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_pinned_comments_pin
			ON pinned_comments(live_name, pin_id)
			WHERE pin_id IS NOT NULL AND pin_id != ''`,
	}

	for _, s := range stmts {
		if _, err := db.conn.Exec(s); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}
	return nil
}

// --- FeedbackRepository ---

// AddFeedback stores a user-provided classification example.
func (db *DB) AddFeedback(comment, category, expected string) (int64, error) {
	comment = strings.TrimSpace(comment)
	category = strings.TrimSpace(strings.ToUpper(category))
	expected = strings.TrimSpace(strings.ToUpper(expected))

	if comment == "" {
		return 0, model.ErrCommentRequired
	}
	if !model.ValidCategory[category] {
		return 0, model.ErrInvalidCategory
	}
	if !model.ValidExpected[expected] {
		return 0, model.ErrInvalidExpected
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
func (db *DB) GetRecentFeedbacks(limit int) ([]model.Feedback, error) {
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

	var out []model.Feedback
	for rows.Next() {
		var f model.Feedback
		if err := rows.Scan(&f.Comment, &f.Category, &f.Expected); err != nil {
			return nil, fmt.Errorf("scan feedback: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// --- AnomalyRepository ---

// LogAnomaly records a moderation decision.
func (db *DB) LogAnomaly(liveName, comment string, isAnomaly bool, category, uniqueID string) error {
	now := time.Now()
	day := now.UTC().Format("2006-01-02")
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
func (db *DB) GetRecentModerations(limit int) ([]model.AnomalyLog, error) {
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

	var out []model.AnomalyLog
	for rows.Next() {
		var a model.AnomalyLog
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

// GetRecentAnomalyLogs retrieves the most recent anomaly logs.
func (db *DB) GetRecentAnomalyLogs(limit int) ([]model.AnomalyLog, error) {
	return db.GetRecentModerations(limit)
}

// GetAnomalyLogsByLiveName retrieves logs for a specific live name.
func (db *DB) GetAnomalyLogsByLiveName(liveName string) ([]model.AnomalyLog, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category FROM anomaly_logs WHERE live_name = ?",
		liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query anomaly logs by live: %w", err)
	}
	defer rows.Close()

	var out []model.AnomalyLog
	for rows.Next() {
		var a model.AnomalyLog
		var isAnomaly bool
		if err := rows.Scan(&a.ID, &a.LiveName, &a.Day, &a.Timestamp, &a.UniqueID, &a.Comment, &isAnomaly, &a.Category); err != nil {
			return nil, fmt.Errorf("scan anomaly log: %w", err)
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
		return 0, model.ErrInvalidID
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

	result, err := db.conn.Exec("DELETE FROM anomaly_logs WHERE day < date('now')")
	if err != nil {
		return 0, fmt.Errorf("cleanup anomalies: %w", err)
	}
	return result.RowsAffected()
}

// --- UserMessageRepository ---

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
func (db *DB) GetUserMessages(uniqueID string) ([]model.UserMessage, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return nil, model.ErrUniqueIDRequired
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

	var out []model.UserMessage
	for rows.Next() {
		var um model.UserMessage
		if err := rows.Scan(&um.ID, &um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
			return nil, fmt.Errorf("scan user message: %w", err)
		}
		out = append(out, um)
	}
	return out, rows.Err()
}

// GetAllUserMessages returns all user messages grouped by user.
func (db *DB) GetAllUserMessages() (map[string][]model.UserMessage, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT uniqueId, username, message, timestamp FROM user_messages ORDER BY uniqueId, timestamp DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("query all user messages: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]model.UserMessage)
	for rows.Next() {
		var um model.UserMessage
		if err := rows.Scan(&um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
			return nil, fmt.Errorf("scan user message: %w", err)
		}
		result[um.UniqueID] = append(result[um.UniqueID], um)
	}
	return result, rows.Err()
}

// --- GiftRepository ---

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
func (db *DB) GetRecentGifts(liveName string, limit int) ([]model.Gift, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		"SELECT id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type, timestamp FROM gifts WHERE live_name = ? ORDER BY timestamp DESC LIMIT ?",
		liveName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query gifts: %w", err)
	}
	defer rows.Close()

	out := make([]model.Gift, 0)
	for rows.Next() {
		var g model.Gift
		if err := rows.Scan(&g.ID, &g.LiveName, &g.UniqueID, &g.Nickname, &g.GiftName, &g.RepeatCount, &g.GiftType, &g.Timestamp); err != nil {
			return nil, fmt.Errorf("scan gift: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGiftsByUser returns all gifts for a specific user.
func (db *DB) GetGiftsByUser(uniqueID string) ([]model.Gift, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return nil, model.ErrUniqueIDRequired
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

	var out []model.Gift
	for rows.Next() {
		var g model.Gift
		if err := rows.Scan(&g.ID, &g.LiveName, &g.UniqueID, &g.Nickname, &g.GiftName, &g.RepeatCount, &g.GiftType, &g.Timestamp); err != nil {
			return nil, fmt.Errorf("scan gift by user: %w", err)
		}
		out = append(out, g)
	}
	if out == nil {
		out = []model.Gift{}
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

// GetTodayUserMessages returns all user messages from today.
func (db *DB) GetTodayUserMessages() ([]model.UserMessage, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		`SELECT id, uniqueId, username, message, timestamp
		 FROM user_messages
		 WHERE date(timestamp) = date('now')
		 ORDER BY timestamp ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query today user messages: %w", err)
	}
	defer rows.Close()

	var out []model.UserMessage
	for rows.Next() {
		var um model.UserMessage
		if err := rows.Scan(&um.ID, &um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
			return nil, fmt.Errorf("scan today user message: %w", err)
		}
		out = append(out, um)
	}
	return out, rows.Err()
}

// GetTodayAnomalyLogs returns anomaly logs from today for the given live name.
func (db *DB) GetTodayAnomalyLogs(liveName string) ([]model.AnomalyLog, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs
		 WHERE day = date('now') AND live_name = ?
		 ORDER BY timestamp ASC`,
		liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query today anomaly logs: %w", err)
	}
	defer rows.Close()

	var out []model.AnomalyLog
	for rows.Next() {
		var a model.AnomalyLog
		var isAnomaly bool
		if err := rows.Scan(&a.ID, &a.LiveName, &a.Day, &a.Timestamp, &a.UniqueID, &a.Comment, &isAnomaly, &a.Category); err != nil {
			return nil, fmt.Errorf("scan today anomaly log: %w", err)
		}
		a.IsAnomaly = isAnomaly
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- TargetGiftHistoryRepository ---

// AddTargetGiftHistory stores a pending target gift history entry.
func (db *DB) AddTargetGiftHistory(liveName, uniqueID, nickname, giftName string, receivedAt time.Time) (int64, error) {
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(
		`INSERT INTO target_gift_history
			(live_name, uniqueId, nickname, gift_name, received_at)
		 VALUES (?, ?, ?, ?, ?)`,
		liveName, uniqueID, nickname, giftName, receivedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("insert target gift history: %w", err)
	}
	return result.LastInsertId()
}

// MarkTargetGiftAnswered sets answered_at/response_type for a pending history entry.
func (db *DB) MarkTargetGiftAnswered(id int64, responseType string, answeredAt time.Time) error {
	if id <= 0 {
		return model.ErrInvalidID
	}
	responseType = strings.TrimSpace(strings.ToLower(responseType))
	if responseType != model.TargetGiftResponseManual && responseType != model.TargetGiftResponseAutomatic {
		return fmt.Errorf("invalid response type %q", responseType)
	}
	if answeredAt.IsZero() {
		answeredAt = time.Now()
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(
		`UPDATE target_gift_history
		 SET answered_at = ?, response_type = ?
		 WHERE id = ? AND answered_at IS NULL`,
		answeredAt.UTC().Format(time.RFC3339Nano), responseType, id,
	)
	if err != nil {
		return fmt.Errorf("mark target gift answered: %w", err)
	}
	return nil
}

// GetRecentTargetGiftHistory returns the latest N target gift history rows.
func (db *DB) GetRecentTargetGiftHistory(liveName string, limit int) ([]model.TargetGiftHistory, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(liveName) == "" {
		rows, err = db.conn.Query(
			`SELECT id, live_name, uniqueId, nickname, gift_name, received_at, answered_at, response_type
			 FROM target_gift_history
			 ORDER BY received_at DESC
			 LIMIT ?`,
			limit,
		)
	} else {
		rows, err = db.conn.Query(
			`SELECT id, live_name, uniqueId, nickname, gift_name, received_at, answered_at, response_type
			 FROM target_gift_history
			 WHERE live_name = ?
			 ORDER BY received_at DESC
			 LIMIT ?`,
			liveName, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query target gift history: %w", err)
	}
	defer rows.Close()

	out := make([]model.TargetGiftHistory, 0)
	for rows.Next() {
		h, err := scanTargetGiftHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetPendingTargetGiftHistory returns unanswered target gift rows, newest first.
func (db *DB) GetPendingTargetGiftHistory(liveName string, limit int) ([]model.TargetGiftHistory, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	liveName = strings.TrimSpace(liveName)
	if liveName == "" {
		return []model.TargetGiftHistory{}, nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		`SELECT id, live_name, uniqueId, nickname, gift_name, received_at, answered_at, response_type
		 FROM target_gift_history
		 WHERE live_name = ? AND answered_at IS NULL
		 ORDER BY received_at DESC
		 LIMIT ?`,
		liveName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending target gifts: %w", err)
	}
	defer rows.Close()

	out := make([]model.TargetGiftHistory, 0)
	for rows.Next() {
		h, err := scanTargetGiftHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func scanTargetGiftHistory(rows *sql.Rows) (model.TargetGiftHistory, error) {
	var (
		h                    model.TargetGiftHistory
		answeredAt, respType sql.NullString
	)
	if err := rows.Scan(
		&h.ID, &h.LiveName, &h.UniqueID, &h.Nickname, &h.GiftName,
		&h.ReceivedAt, &answeredAt, &respType,
	); err != nil {
		return model.TargetGiftHistory{}, fmt.Errorf("scan target gift history: %w", err)
	}
	if answeredAt.Valid {
		v := answeredAt.String
		h.AnsweredAt = &v
	}
	if respType.Valid {
		v := respType.String
		h.ResponseType = &v
	}
	return h, nil
}

// --- PinnedCommentRepository ---

// AddPinnedComment stores a pinned comment. Duplicate pin_id for the same live is ignored.
func (db *DB) AddPinnedComment(liveName, uniqueID, nickname, comment, pinID string, isFollower *bool, at time.Time) (int64, error) {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return 0, model.ErrCommentRequired
	}
	if uniqueID == "" {
		uniqueID = "unknown"
	}
	if nickname == "" {
		nickname = uniqueID
	}
	pinID = strings.TrimSpace(pinID)
	if at.IsZero() {
		at = time.Now()
	}

	var follower any
	if isFollower != nil {
		if *isFollower {
			follower = 1
		} else {
			follower = 0
		}
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	if pinID != "" {
		var existing int64
		err := db.conn.QueryRow(
			`SELECT id FROM pinned_comments WHERE live_name = ? AND pin_id = ?`,
			liveName, pinID,
		).Scan(&existing)
		if err == nil {
			return existing, nil
		}
		if err != sql.ErrNoRows {
			return 0, fmt.Errorf("lookup pinned comment: %w", err)
		}
	}

	result, err := db.conn.Exec(
		`INSERT INTO pinned_comments
			(live_name, uniqueId, nickname, comment, pin_id, is_follower, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		liveName, uniqueID, nickname, comment, nullIfEmpty(pinID), follower, at.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("insert pinned comment: %w", err)
	}
	return result.LastInsertId()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// GetRecentPinnedComments returns the latest N pinned comments.
func (db *DB) GetRecentPinnedComments(liveName string, limit int) ([]model.PinnedComment, error) {
	if limit < 1 || limit > 200 {
		limit = 15
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(liveName) == "" {
		rows, err = db.conn.Query(
			`SELECT id, live_name, uniqueId, nickname, comment, pin_id, is_follower, timestamp
			 FROM pinned_comments
			 ORDER BY timestamp DESC
			 LIMIT ?`,
			limit,
		)
	} else {
		rows, err = db.conn.Query(
			`SELECT id, live_name, uniqueId, nickname, comment, pin_id, is_follower, timestamp
			 FROM pinned_comments
			 WHERE live_name = ?
			 ORDER BY timestamp DESC
			 LIMIT ?`,
			liveName, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query pinned comments: %w", err)
	}
	defer rows.Close()

	out := make([]model.PinnedComment, 0)
	for rows.Next() {
		var (
			c        model.PinnedComment
			pinID    sql.NullString
			follower sql.NullInt64
		)
		if err := rows.Scan(
			&c.ID, &c.LiveName, &c.UniqueID, &c.Nickname, &c.Comment,
			&pinID, &follower, &c.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("scan pinned comment: %w", err)
		}
		if pinID.Valid {
			c.PinID = pinID.String
		}
		if follower.Valid {
			b := follower.Int64 != 0
			c.IsFollower = &b
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- SessionRepository ---

func parseSQLiteTime(s string) (time.Time, error) {
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
	err := db.conn.QueryRow(query, args...).Scan(&raw)
	if err != nil {
		return time.Time{}, false, err
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, false, nil
	}
	ts, err := parseSQLiteTime(raw.String)
	if err != nil {
		return time.Time{}, false, err
	}
	return ts, true, nil
}

// GetLastSessionActivity returns the latest timestamp among gifts and
// anomaly logs for liveName, plus all user_messages (which have no live_name).
func (db *DB) GetLastSessionActivity(liveName string) (time.Time, bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	queries := []struct {
		q    string
		args []any
	}{
		{"SELECT MAX(timestamp) FROM gifts WHERE live_name = ?", []any{liveName}},
		{"SELECT MAX(timestamp) FROM anomaly_logs WHERE live_name = ?", []any{liveName}},
		{"SELECT MAX(timestamp) FROM user_messages", nil},
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

// DeleteSessionData removes gifts and anomaly logs for liveName and all user_messages.
func (db *DB) DeleteSessionData(liveName string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM gifts WHERE live_name = ?", liveName); err != nil {
		return fmt.Errorf("delete session gifts: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM anomaly_logs WHERE live_name = ?", liveName); err != nil {
		return fmt.Errorf("delete session anomalies: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM user_messages"); err != nil {
		return fmt.Errorf("delete session messages: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM pinned_comments WHERE live_name = ?", liveName); err != nil {
		return fmt.Errorf("delete session pinned comments: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM target_gift_history WHERE live_name = ?", liveName); err != nil {
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
	_, err := db.conn.Exec(query, args...)
	return err
}

// --- Repository interface ---

var _ model.Repository = (*DB)(nil)

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}
