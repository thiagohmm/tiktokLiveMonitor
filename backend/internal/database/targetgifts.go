package database

import (
	"database/sql"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"strings"
	"time"
)

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
	defer closeRows(rows)

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
	defer closeRows(rows)

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
