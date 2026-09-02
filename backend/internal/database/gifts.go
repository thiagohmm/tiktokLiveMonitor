package database

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

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
	defer closeRows(rows)

	out := make([]model.Gift, 0)
	for rows.Next() {
		g, err := scanGift(rows)
		if err != nil {
			return nil, err
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
	defer closeRows(rows)

	var out []model.Gift
	for rows.Next() {
		g, err := scanGift(rows)
		if err != nil {
			return nil, err
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
	defer closeRows(rows)

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

// scanGift reads one gifts row into a model.Gift.
func scanGift(rows *sql.Rows) (model.Gift, error) {
	var g model.Gift
	if err := rows.Scan(&g.ID, &g.LiveName, &g.UniqueID, &g.Nickname, &g.GiftName, &g.RepeatCount, &g.GiftType, &g.Timestamp); err != nil {
		return model.Gift{}, fmt.Errorf("scan gift: %w", err)
	}
	return g, nil
}
