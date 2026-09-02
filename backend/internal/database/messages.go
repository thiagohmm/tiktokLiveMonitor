package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

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
	// Rollback is a no-op once the transaction commits; its error is not
	// actionable, so it is intentionally ignored.
	defer func() { _ = tx.Rollback() }()

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
	defer closeRows(rows)

	var out []model.UserMessage
	for rows.Next() {
		um, err := scanUserMessage(rows)
		if err != nil {
			return nil, err
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
	defer closeRows(rows)

	result := make(map[string][]model.UserMessage)
	for rows.Next() {
		um, err := scanUserMessage(rows)
		if err != nil {
			return nil, err
		}
		result[um.UniqueID] = append(result[um.UniqueID], um)
	}
	return result, rows.Err()
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
	defer closeRows(rows)

	var out []model.UserMessage
	for rows.Next() {
		um, err := scanUserMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, um)
	}
	return out, rows.Err()
}

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
	defer closeRows(rows)

	var out []model.UserMessage
	for rows.Next() {
		um, err := scanUserMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, um)
	}
	return out, rows.Err()
}

// scanUserMessage reads one user_messages row into a model.UserMessage.
func scanUserMessage(rows *sql.Rows) (model.UserMessage, error) {
	var um model.UserMessage
	if err := rows.Scan(&um.ID, &um.LiveName, &um.UniqueID, &um.Username, &um.Message, &um.Timestamp); err != nil {
		return model.UserMessage{}, fmt.Errorf("scan user message: %w", err)
	}
	return um, nil
}
