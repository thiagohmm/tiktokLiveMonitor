// Package database provides the PostgreSQL (Supabase) implementation of the model.Repository interface.
package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// DB wraps the database connection with thread-safe access and implements model.Repository.
type DB struct {
	conn *sql.DB
	mu   sync.Mutex
}

// --- FeedbackRepository ---

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
	defer rows.Close()

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

// --- AnomalyRepository ---

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

	rows, err := db.query(
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

// --- UserMessageRepository ---

// AddUserMessageDedup stores a user message only if it's unique for that user, keeping max 10.
func (db *DB) AddUserMessageDedup(liveName, uniqueID, username, message string) error {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	message = strings.ToLower(strings.TrimSpace(message))
	username = strings.TrimSpace(username)
	liveName = strings.TrimSpace(liveName)

	if message == "" || uniqueID == "" {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	// Check if message already exists for this user.
	var count int
	err := db.queryRow(
		"SELECT COUNT(*) FROM user_messages WHERE LOWER(uniqueId) = ? AND LOWER(message) = ?",
		uniqueID, message,
	).Scan(&count)
	if err != nil || count > 0 {
		return err
	}

	// Insert new message.
	_, err = db.exec(
		"INSERT INTO user_messages (live_name, uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?, ?)",
		liveName, uniqueID, username, message, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert user message: %w", err)
	}

	// Keep only the 10 most recent unique messages per user.
	_, err = db.exec(`
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

// UserMessageEntry is a pending user message to be stored in a batch.
type UserMessageEntry struct {
	LiveName  string
	UniqueID  string
	Username  string
	Message   string
	Timestamp time.Time
}

// BatchAddUserMessages inserts multiple user messages in a single transaction.
// Duplicates (same user, same message, case-insensitive) are skipped and every
// affected user is pruned to their 10 most recent messages.
func (db *DB) BatchAddUserMessages(entries []UserMessageEntry) error {
	if len(entries) == 0 {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin batch user messages: %w", err)
	}
	defer tx.Rollback()

	users := make(map[string]struct{})
	for _, e := range entries {
		uid := strings.ToLower(strings.TrimSpace(e.UniqueID))
		msg := strings.ToLower(strings.TrimSpace(e.Message))
		if uid == "" || msg == "" {
			continue
		}
		username := strings.TrimSpace(e.Username)
		if username == "" {
			username = uid
		}
		liveName := strings.TrimSpace(e.LiveName)
		ts := e.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		_, err := tx.Exec(db.bind(`
			INSERT INTO user_messages (live_name, uniqueId, username, message, timestamp)
			SELECT ?, ?, ?, ?, ? WHERE NOT EXISTS (
				SELECT 1 FROM user_messages WHERE LOWER(uniqueId) = ? AND LOWER(message) = ?
			)`), liveName, e.UniqueID, username, e.Message, ts, uid, msg)
		if err != nil {
			return fmt.Errorf("batch insert user message: %w", err)
		}
		users[uid] = struct{}{}
	}

	for uid := range users {
		_, err := tx.Exec(db.bind(`
			DELETE FROM user_messages WHERE id NOT IN (
				SELECT id FROM user_messages WHERE LOWER(uniqueId) = ?
				ORDER BY timestamp DESC LIMIT 10
			) AND LOWER(uniqueId) = ?`), uid, uid)
		if err != nil {
			return fmt.Errorf("prune user messages: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch user messages: %w", err)
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

	rows, err := db.query(
		"SELECT id, live_name, uniqueId, username, message, timestamp FROM user_messages WHERE LOWER(uniqueId) = ? ORDER BY timestamp DESC",
		uniqueID,
	)
	if err != nil {
		return nil, fmt.Errorf("query user messages: %w", err)
	}
	defer rows.Close()

	var out []model.UserMessage
	for rows.Next() {
		var um model.UserMessage
		if err := rows.Scan(&um.ID, &um.LiveName, &um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
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

	rows, err := db.query(
		"SELECT id, live_name, uniqueId, username, message, timestamp FROM user_messages ORDER BY uniqueId, timestamp DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("query all user messages: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]model.UserMessage)
	for rows.Next() {
		var um model.UserMessage
		if err := rows.Scan(&um.ID, &um.LiveName, &um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
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

	result, err := db.insertID(
		"INSERT INTO gifts (live_name, uniqueId, nickname, gift_name, repeat_count, gift_type) VALUES (?, ?, ?, ?, ?, ?)",
		liveName, uniqueID, nickname, giftName, repeatCount, giftType,
	)
	if err != nil {
		return 0, fmt.Errorf("insert gift: %w", err)
	}
	return result, nil
}

// AddShare stores a share of the live made by a participant.
func (db *DB) AddShare(liveName, uniqueID, nickname string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, err := db.exec(
		"INSERT INTO shares (live_name, uniqueId, nickname) VALUES (?, ?, ?)",
		liveName, uniqueID, nickname,
	); err != nil {
		return fmt.Errorf("insert share: %w", err)
	}
	return nil
}

// AddLike stores a batch of likes (hearts) sent by a participant during a live.
// likeCount is the number of likes in this event (usually 1, but events can carry
// a burst). The per-user total is obtained by SUM(like_count).
func (db *DB) AddLike(liveName, uniqueID, nickname string, likeCount int) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if likeCount < 1 {
		likeCount = 1
	}

	if _, err := db.exec(
		"INSERT INTO likes (live_name, uniqueId, nickname, like_count) VALUES (?, ?, ?, ?)",
		liveName, uniqueID, nickname, likeCount,
	); err != nil {
		return fmt.Errorf("insert like: %w", err)
	}
	return nil
}

// UpsertRoomLikeTotal stores the room-level cumulative like counter reported
// by the stream. The stream value is monotonic per live, so only the highest
// value seen is kept.
func (db *DB) UpsertRoomLikeTotal(liveName string, total int64) error {
	if total <= 0 {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.upsertRoomLikeTotal(liveName, total); err != nil {
		return fmt.Errorf("upsert room like total: %w", err)
	}
	return nil
}

// LikeTotals returns the room-level cumulative like total (as reported by the
// stream) and the sum of per-event likes delivered by the stream for a live.
// roomTotal is 0 when the stream never reported one.
func (db *DB) LikeTotals(liveName string) (int64, int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var roomTotal int64
	if err := db.queryRow(
		"SELECT COALESCE(MAX(total), 0) FROM room_like_totals WHERE live_name = ?", liveName,
	).Scan(&roomTotal); err != nil {
		return 0, 0, fmt.Errorf("query room like total: %w", err)
	}

	var delivered int64
	if err := db.queryRow(
		"SELECT COALESCE(SUM(like_count), 0) FROM likes WHERE live_name = ?", liveName,
	).Scan(&delivered); err != nil {
		return 0, 0, fmt.Errorf("query delivered likes: %w", err)
	}
	return roomTotal, delivered, nil
}

// GetRecentGifts returns the latest N gifts.
func (db *DB) GetRecentGifts(liveName string, limit int) ([]model.Gift, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
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

	rows, err := db.query(
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

	rows, err := db.query(
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

	result, err := db.exec("DELETE FROM gifts")
	if err != nil {
		return 0, fmt.Errorf("clear gifts: %w", err)
	}
	return result.RowsAffected()
}

// GetGiftUnits returns the total gift units (SUM repeat_count) and the
// number of gift events recorded for a live. When no gift names are given
// (or only empty strings), all gifts count; otherwise only events whose
// gift_name matches one of the given names count.
func (db *DB) GetGiftUnits(liveName string, giftNames ...string) (units, count int, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	query := "SELECT COALESCE(SUM(repeat_count), 0), COUNT(*) FROM gifts WHERE live_name = ?"
	args := []interface{}{liveName}
	seen := make(map[string]bool)
	for _, name := range giftNames {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		args = append(args, name)
	}
	if len(args) > 1 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)-1), ",")
		query += " AND gift_name IN (" + placeholders + ")"
	}
	if err := db.queryRow(query, args...).Scan(&units, &count); err != nil {
		return 0, 0, fmt.Errorf("query gift units: %w", err)
	}
	return units, count, nil
}

// GetTodayUserMessages returns today's user messages for the given live.
func (db *DB) GetTodayUserMessages(liveName string) ([]model.UserMessage, error) {
	liveName = strings.TrimSpace(liveName)
	if liveName == "" {
		return []model.UserMessage{}, nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT id, live_name, uniqueId, username, message, timestamp
		 FROM user_messages
		 WHERE live_name = ? AND date(timestamp) = date('now')
		 ORDER BY timestamp ASC`,
		liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query today user messages: %w", err)
	}
	defer rows.Close()

	var out []model.UserMessage
	for rows.Next() {
		var um model.UserMessage
		if err := rows.Scan(&um.ID, &um.LiveName, &um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
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

	id, err := db.insertID(
		`INSERT INTO target_gift_history
			(live_name, uniqueId, nickname, gift_name, received_at)
		 VALUES (?, ?, ?, ?, ?)`,
		liveName, uniqueID, nickname, giftName, receivedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("insert target gift history: %w", err)
	}
	return id, nil
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

	_, err := db.exec(
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
		rows, err = db.query(
			`SELECT id, live_name, uniqueId, nickname, gift_name, received_at, answered_at, response_type
			 FROM target_gift_history
			 ORDER BY received_at DESC
			 LIMIT ?`,
			limit,
		)
	} else {
		rows, err = db.query(
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

	rows, err := db.query(
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

// --- GoalRepository ---

// AddGiftGoal stores a new gift goal and returns its id.
func (db *DB) AddGiftGoal(g model.GiftGoal) (int64, error) {
	if strings.TrimSpace(g.Title) == "" {
		return 0, fmt.Errorf("goal title is required")
	}
	if g.TargetUnits < 1 {
		return 0, fmt.Errorf("target units must be >= 1")
	}
	if strings.TrimSpace(g.LiveName) == "" {
		return 0, fmt.Errorf("live name is required")
	}
	if g.Status == "" {
		g.Status = model.GoalStatusActive
	}
	if g.Milestones == nil {
		g.Milestones = []model.GoalMilestone{}
	}
	if g.CreatedAt == "" {
		g.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	milestonesJSON, err := json.Marshal(g.Milestones)
	if err != nil {
		return 0, fmt.Errorf("marshal goal milestones: %w", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	id, err := db.insertID(
		`INSERT INTO gift_goals (live_name, title, gift_name, target_units, status, milestones, created_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		g.LiveName, g.Title, g.GiftName, g.TargetUnits, g.Status, string(milestonesJSON),
		g.CreatedAt, nullTime(g.CompletedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert gift goal: %w", err)
	}
	return id, nil
}

// GetGiftGoals returns all goals for a live, newest first.
func (db *DB) GetGiftGoals(liveName string) ([]model.GiftGoal, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT id, live_name, title, gift_name, target_units, status, milestones, created_at, completed_at
		 FROM gift_goals
		 WHERE live_name = ?
		 ORDER BY id DESC`,
		liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query gift goals: %w", err)
	}
	defer rows.Close()

	out := make([]model.GiftGoal, 0)
	for rows.Next() {
		g, err := scanGiftGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveGiftGoal persists an existing goal's mutable fields (title, target,
// status, milestones, completed_at).
func (db *DB) SaveGiftGoal(g model.GiftGoal) error {
	if g.ID <= 0 {
		return model.ErrInvalidID
	}
	milestonesJSON, err := json.Marshal(g.Milestones)
	if err != nil {
		return fmt.Errorf("marshal goal milestones: %w", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err = db.exec(
		`UPDATE gift_goals
		 SET title = ?, gift_name = ?, target_units = ?, status = ?, milestones = ?, completed_at = ?
		 WHERE id = ?`,
		g.Title, g.GiftName, g.TargetUnits, g.Status, string(milestonesJSON), nullTime(g.CompletedAt), g.ID,
	)
	if err != nil {
		return fmt.Errorf("save gift goal: %w", err)
	}
	return nil
}

// DeleteGiftGoals removes all goals for a live and returns the rows deleted.
func (db *DB) DeleteGiftGoals(liveName string) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.exec("DELETE FROM gift_goals WHERE live_name = ?", liveName)
	if err != nil {
		return 0, fmt.Errorf("delete gift goals: %w", err)
	}
	return result.RowsAffected()
}

func scanGiftGoal(rows *sql.Rows) (model.GiftGoal, error) {
	var (
		g           model.GiftGoal
		milestones  string
		completedAt sql.NullString
	)
	if err := rows.Scan(&g.ID, &g.LiveName, &g.Title, &g.GiftName, &g.TargetUnits, &g.Status, &milestones, &g.CreatedAt, &completedAt); err != nil {
		return model.GiftGoal{}, fmt.Errorf("scan gift goal: %w", err)
	}
	if err := json.Unmarshal([]byte(milestones), &g.Milestones); err != nil {
		return model.GiftGoal{}, fmt.Errorf("unmarshal goal milestones: %w", err)
	}
	if g.Milestones == nil {
		g.Milestones = []model.GoalMilestone{}
	}
	if completedAt.Valid {
		v := normalizeTime(completedAt.String)
		g.CompletedAt = &v
	}
	g.CreatedAt = normalizeTime(g.CreatedAt)
	return g, nil
}

// nullTime converts a pointer timestamp to a nullable argument (nil stays NULL).
func nullTime(p *string) interface{} {
	if p == nil {
		return nil
	}
	v := *p
	return v
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
		err := db.queryRow(
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

	id, err := db.insertID(
		`INSERT INTO pinned_comments
			(live_name, uniqueId, nickname, comment, pin_id, is_follower, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		liveName, uniqueID, nickname, comment, nullIfEmpty(pinID), follower, at.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("insert pinned comment: %w", err)
	}
	return id, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// coalesceStr returns the first non-empty string, or the fallback.
func coalesceStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
		rows, err = db.query(
			`SELECT id, live_name, uniqueId, nickname, comment, pin_id, is_follower, timestamp
			 FROM pinned_comments
			 ORDER BY timestamp DESC
			 LIMIT ?`,
			limit,
		)
	} else {
		rows, err = db.query(
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
	defer tx.Rollback()

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

// --- RankingRepository ---

// LiveFirstSeen returns the earliest recorded timestamp for a live.
func (db *DB) LiveFirstSeen(liveName string) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var ts sql.NullString
	query := `SELECT MIN(timestamp) FROM (
		SELECT timestamp FROM user_messages WHERE live_name = ?
		UNION ALL SELECT timestamp FROM gifts WHERE live_name = ?
		UNION ALL SELECT timestamp FROM likes WHERE live_name = ?
		UNION ALL SELECT received_at FROM target_gift_history WHERE live_name = ?
		UNION ALL SELECT timestamp FROM anomaly_logs WHERE live_name = ?
	)`
	err := db.queryRow(query, liveName, liveName, liveName, liveName, liveName).Scan(&ts)
	if err != nil {
		return "", fmt.Errorf("query live first seen: %w", err)
	}
	if !ts.Valid || ts.String == "" {
		return "", nil
	}
	at, perr := parseStoredTime(ts.String)
	if perr != nil {
		return ts.String, nil
	}
	return at.UTC().Format(time.RFC3339), nil
}

// ListLives returns derived lives grouped by live_name and day, most recent first.
func (db *DB) ListLives(limit int) ([]model.Live, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT live_name, day, MIN(ts) AS started_at, MAX(ts) AS ended_at, COUNT(*) AS events
		FROM (
			SELECT live_name, DATE(timestamp) AS day, timestamp AS ts FROM user_messages
			UNION ALL
			SELECT live_name, DATE(timestamp), timestamp FROM gifts
			UNION ALL
			SELECT live_name, DATE(timestamp), timestamp FROM shares
			UNION ALL
			SELECT live_name, DATE(timestamp), timestamp FROM anomaly_logs
			UNION ALL
			SELECT live_name, DATE(timestamp), timestamp FROM pinned_comments
			UNION ALL
			SELECT live_name, DATE(received_at), received_at FROM target_gift_history
		)
		WHERE live_name != ''
		GROUP BY live_name, day
		ORDER BY day DESC, started_at DESC
		LIMIT ?`

	rows, err := db.query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("query lives: %w", err)
	}
	defer rows.Close()

	results := []model.Live{}
	for rows.Next() {
		var (
			l    model.Live
			s, e sql.NullString
		)
		if err := rows.Scan(&l.Name, &l.Day, &s, &e, &l.Events); err != nil {
			return nil, fmt.Errorf("scan live: %w", err)
		}
		l.Day = normalizeDate(l.Day)
		if s.Valid {
			l.StartedAt = normalizeTime(s.String)
		}
		if e.Valid {
			l.EndedAt = normalizeTime(e.String)
		}
		results = append(results, l)
	}
	return results, rows.Err()
}

// DeleteLive removes all rows for a live from every table that stores live_name.
// It returns the total number of rows deleted.
func (db *DB) DeleteLive(liveName string) (int64, error) {
	if strings.TrimSpace(liveName) == "" {
		return 0, fmt.Errorf("live name is required")
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	tables := []string{"user_messages", "gifts", "likes", "room_like_totals", "shares", "anomaly_logs", "pinned_comments", "target_gift_history", "gift_goals"}
	total := int64(0)
	for _, table := range tables {
		res, err := db.exec(fmt.Sprintf("DELETE FROM %s WHERE live_name = ?", table), liveName)
		if err != nil {
			return total, fmt.Errorf("delete from %s: %w", table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("rows affected in %s: %w", table, err)
		}
		total += n
	}
	return total, nil
}

// normalizeTime converts a stored timestamp string to RFC3339 UTC when possible.
func normalizeTime(raw string) string {
	at, err := parseStoredTime(raw)
	if err != nil {
		return raw
	}
	return at.UTC().Format(time.RFC3339)
}

// normalizeDate reduz um DATE escaneado como timestamp (ex.:
// "2026-08-24T00:00:00Z") para "YYYY-MM-DD".
func normalizeDate(day string) string {
	day = strings.TrimSpace(day)
	if len(day) >= 10 && day[4] == '-' && day[7] == '-' {
		return day[:10]
	}
	if t, err := parseStoredTime(day); err == nil {
		return t.Format("2006-01-02")
	}
	return day
}

// LiveStatsByUser returns per-user aggregated stats for a live.
func (db *DB) LiveStatsByUser(liveName string) ([]model.LiveStat, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stats := make(map[string]model.LiveStat)

	// Messages (and questions, detected heuristically).
	msgRows, err := db.query(`
		SELECT uniqueId, COALESCE((array_agg(username ORDER BY timestamp DESC))[1], '') AS username, COUNT(*) AS n,
			SUM(CASE WHEN strpos(message, '?') > 0 OR lower(message) LIKE 'pq%'
			  OR lower(message) LIKE 'por que%' OR lower(message) LIKE 'como%'
			  OR lower(message) LIKE 'quando%' OR lower(message) LIKE 'qual%'
			  OR lower(message) LIKE 'quem%' OR lower(message) LIKE 'onde%'
			  OR lower(message) LIKE 'aonde%' OR lower(message) LIKE 'sera%'
			  OR lower(message) LIKE 'pode%' OR lower(message) LIKE 'poderia%'
			THEN 1 ELSE 0 END) AS questions,
		MIN(timestamp) AS first, MAX(timestamp) AS last
		FROM user_messages WHERE live_name = ? GROUP BY uniqueId`, liveName)
	if err != nil {
		return nil, fmt.Errorf("query live stats messages: %w", err)
	}
	for msgRows.Next() {
		var uid, uname, first, last sql.NullString
		var n, questions int
		if err := msgRows.Scan(&uid, &uname, &n, &questions, &first, &last); err != nil {
			msgRows.Close()
			return nil, fmt.Errorf("scan live stat message: %w", err)
		}
		if uid.String == "" {
			continue
		}
		s := stats[uid.String]
		s.UniqueID = uid.String
		s.Nickname = coalesceStr(uname.String, uid.String)
		s.MessageCount = n
		s.QuestionCount = questions
		if first.Valid && first.String != "" {
			s.FirstSeen = first.String
		}
		if last.Valid && last.String != "" {
			s.LastSeen = last.String
		}
		stats[uid.String] = s
	}
	msgRows.Close()

	// Gifts.
	giftRows, err := db.query(`
		SELECT uniqueId, COALESCE((array_agg(nickname ORDER BY timestamp DESC))[1], '') AS nickname, COUNT(*) AS n, SUM(repeat_count) AS total,
			MIN(timestamp) AS first, MAX(timestamp) AS last
		FROM gifts WHERE live_name = ? GROUP BY uniqueId`, liveName)
	if err != nil {
		return nil, fmt.Errorf("query live stats gifts: %w", err)
	}
	for giftRows.Next() {
		var uid, uname string
		var n, total int
		var first, last sql.NullString
		if err := giftRows.Scan(&uid, &uname, &n, &total, &first, &last); err != nil {
			giftRows.Close()
			return nil, fmt.Errorf("scan live stat gift: %w", err)
		}
		if uid == "" {
			continue
		}
		s := stats[uid]
		s.UniqueID = uid
		s.Nickname = coalesceStr(uname, uid)
		s.GiftCount = n
		s.GiftTotal = total
		if first.Valid && first.String != "" {
			if s.FirstSeen == "" || first.String < s.FirstSeen {
				s.FirstSeen = first.String
			}
		}
		if last.Valid && last.String != "" {
			if s.LastSeen == "" || last.String > s.LastSeen {
				s.LastSeen = last.String
			}
		}
		stats[uid] = s
	}
	giftRows.Close()

	// Shares.
	shareRows, err := db.query(`
		SELECT uniqueId, COALESCE((array_agg(nickname ORDER BY timestamp DESC))[1], '') AS nickname, COUNT(*) AS n,
			MIN(timestamp) AS first, MAX(timestamp) AS last
		FROM shares WHERE live_name = ? GROUP BY uniqueId`, liveName)
	if err != nil {
		return nil, fmt.Errorf("query live stats shares: %w", err)
	}
	for shareRows.Next() {
		var uid, uname string
		var n int
		var first, last sql.NullString
		if err := shareRows.Scan(&uid, &uname, &n, &first, &last); err != nil {
			shareRows.Close()
			return nil, fmt.Errorf("scan live stat share: %w", err)
		}
		if uid == "" {
			continue
		}
		s := stats[uid]
		s.UniqueID = uid
		s.Nickname = coalesceStr(uname, uid)
		s.ShareCount = n
		if first.Valid && first.String != "" {
			if s.FirstSeen == "" || first.String < s.FirstSeen {
				s.FirstSeen = first.String
			}
		}
		if last.Valid && last.String != "" {
			if s.LastSeen == "" || last.String > s.LastSeen {
				s.LastSeen = last.String
			}
		}
		stats[uid] = s
	}
	shareRows.Close()

	// Likes (hearts).
	likeRows, err := db.query(`
		SELECT uniqueId, COALESCE((array_agg(nickname ORDER BY timestamp DESC))[1], '') AS nickname, SUM(like_count) AS n,
			MIN(timestamp) AS first, MAX(timestamp) AS last
		FROM likes WHERE live_name = ? GROUP BY uniqueId`, liveName)
	if err != nil {
		return nil, fmt.Errorf("query live stats likes: %w", err)
	}
	for likeRows.Next() {
		var uid, uname string
		var n int
		var first, last sql.NullString
		if err := likeRows.Scan(&uid, &uname, &n, &first, &last); err != nil {
			likeRows.Close()
			return nil, fmt.Errorf("scan live stat like: %w", err)
		}
		if uid == "" {
			continue
		}
		s := stats[uid]
		s.UniqueID = uid
		s.Nickname = coalesceStr(uname, uid)
		s.LikeCount = n
		if first.Valid && first.String != "" {
			if s.FirstSeen == "" || first.String < s.FirstSeen {
				s.FirstSeen = first.String
			}
		}
		if last.Valid && last.String != "" {
			if s.LastSeen == "" || last.String > s.LastSeen {
				s.LastSeen = last.String
			}
		}
		stats[uid] = s
	}
	likeRows.Close()

	out := make([]model.LiveStat, 0, len(stats))
	for _, s := range stats {
		out = append(out, s)
	}
	return out, nil
}

// RecentLivesForUser returns the last N lives a participant appeared in.
func (db *DB) RecentLivesForUser(uniqueID string, limit int) ([]model.UserLiveSummary, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return []model.UserLiveSummary{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	out := make([]model.UserLiveSummary, 0)

	// Group by live_name across messages, gifts and target gifts.
	rows, err := db.query(`
		SELECT live_name,
			SUM(CASE WHEN tbl='msg' THEN 1 ELSE 0 END) AS messages,
			SUM(CASE WHEN tbl='gift' THEN 1 ELSE 0 END) AS gifts,
			MIN(ts) AS first_seen, MAX(ts) AS last_seen
		FROM (
			SELECT live_name, 'msg' AS tbl, uniqueId AS uid, timestamp AS ts
			FROM user_messages WHERE LOWER(uniqueId) = ?
			UNION ALL
			SELECT live_name, 'gift' AS tbl, uniqueId AS uid, timestamp AS ts
			FROM gifts WHERE LOWER(uniqueId) = ?
			UNION ALL
			SELECT live_name, 'gift' AS tbl, uniqueId AS uid, received_at AS ts
			FROM target_gift_history WHERE LOWER(uniqueId) = ?
		) GROUP BY live_name ORDER BY MAX(ts) DESC LIMIT ?`,
		uniqueID, uniqueID, uniqueID, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent lives: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var liveName string
		var messages, gifts int
		var firstSeen, lastSeen sql.NullString
		if err := rows.Scan(&liveName, &messages, &gifts, &firstSeen, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan recent life: %w", err)
		}
		ls := model.UserLiveSummary{LiveName: liveName, Messages: messages, Gifts: gifts}
		if firstSeen.Valid && firstSeen.String != "" {
			if at, perr := parseStoredTime(firstSeen.String); perr == nil {
				ls.FirstSeen = at.UTC().Format(time.RFC3339)
			} else {
				ls.FirstSeen = firstSeen.String
			}
		}
		if lastSeen.Valid && lastSeen.String != "" {
			if at, perr := parseStoredTime(lastSeen.String); perr == nil {
				ls.LastSeen = at.UTC().Format(time.RFC3339)
			} else {
				ls.LastSeen = lastSeen.String
			}
		}
		out = append(out, ls)
	}
	return out, rows.Err()
}

// TotalDistinctUsers counts distinct users across all stored messages.
func (db *DB) TotalDistinctUsers() (int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var count int
	err := db.queryRow(
		"SELECT COUNT(DISTINCT uniqueId) FROM user_messages",
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("query distinct users: %w", err)
	}
	return count, nil
}

// --- SettingsRepository ---

// GetSetting returns the stored value for a settings key. An empty string and
// nil error are returned when the key does not exist yet.
func (db *DB) GetSetting(key string) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var value string
	err := db.queryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get setting %s: %w", key, err)
	}
	return value, nil
}

// SetSetting upserts a settings key/value pair.
func (db *DB) SetSetting(key, value string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) "+
			"ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// --- User engagement lookups (profiles) ---

// GetUserMessagesRecent returns the last `limit` messages of a user, newest
// first. A limit <= 0 returns all messages.
func (db *DB) GetUserMessagesRecent(uniqueID string, limit int) ([]model.UserMessage, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return nil, model.ErrUniqueIDRequired
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	query := "SELECT id, live_name, uniqueId, username, message, timestamp FROM user_messages WHERE LOWER(uniqueId) = ? ORDER BY timestamp DESC"
	if limit > 0 {
		query += " LIMIT ?"
	}
	args := []any{uniqueID}
	if limit > 0 {
		args = append(args, limit)
	}

	rows, err := db.query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query recent user messages: %w", err)
	}
	defer rows.Close()

	var out []model.UserMessage
	for rows.Next() {
		var um model.UserMessage
		if err := rows.Scan(&um.ID, &um.LiveName, &um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
			return nil, fmt.Errorf("scan user message: %w", err)
		}
		out = append(out, um)
	}
	return out, rows.Err()
}

// GetUserShareCount returns the total number of share events made by a user.
func (db *DB) GetUserShareCount(uniqueID string) (int, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return 0, model.ErrUniqueIDRequired
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	var count int
	err := db.queryRow(
		"SELECT COUNT(*) FROM shares WHERE LOWER(uniqueId) = ?",
		uniqueID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count user shares: %w", err)
	}
	return count, nil
}

// GetUserLikeTotal returns the sum of like_count over all like events of a user.
func (db *DB) GetUserLikeTotal(uniqueID string) (int64, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return 0, model.ErrUniqueIDRequired
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	var total int64
	err := db.queryRow(
		"SELECT COALESCE(SUM(like_count), 0) FROM likes WHERE LOWER(uniqueId) = ?",
		uniqueID,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum user likes: %w", err)
	}
	return total, nil
}

// --- Repository interface ---

var _ model.Repository = (*DB)(nil)

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}
