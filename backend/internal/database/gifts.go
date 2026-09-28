package database

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// AddGift stores a gift received during a live stream (one session).
func (db *DB) AddGift(ref model.LiveRef, uniqueID, nickname, giftName string, repeatCount, giftType int) (int64, error) {
	if !ref.Valid() {
		return 0, model.ErrInvalidID
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.insertID(
		"INSERT INTO gifts (live_id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type) VALUES (?, ?, ?, ?, ?, ?, ?)",
		ref.ID, strings.TrimSpace(ref.Name), uniqueID, nickname, giftName, repeatCount, giftType,
	)
	if err != nil {
		return 0, fmt.Errorf("insert gift: %w", err)
	}
	return result, nil
}

// GetRecentGifts returns the latest N gifts.
func (db *DB) GetRecentGifts(orgID, liveName string, limit int) ([]model.Gift, error) {
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
		"SELECT id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type, timestamp FROM gifts WHERE "+orgSessions+" AND live_name = ? ORDER BY timestamp DESC LIMIT ?",
		orgID, liveName, limit,
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
func (db *DB) GetGiftsByUser(orgID, uniqueID string) ([]model.Gift, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return nil, err
	}
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return nil, model.ErrUniqueIDRequired
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		"SELECT id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type, timestamp FROM gifts WHERE "+orgSessions+" AND LOWER(uniqueId) = ? ORDER BY timestamp DESC",
		orgID, uniqueID,
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

// GetGiftSummary returns a summary of the organization's gifts grouped by user.
func (db *DB) GetGiftSummary(orgID string) (map[string]map[string]int, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return nil, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		"SELECT uniqueId, nickname, gift_name, SUM(repeat_count) as total FROM gifts WHERE "+orgSessions+" GROUP BY uniqueId, nickname, gift_name ORDER BY total DESC",
		orgID,
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

// ClearGifts removes all gift records of an organization.
func (db *DB) ClearGifts(orgID string) (int64, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return 0, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.exec("DELETE FROM gifts WHERE "+orgSessions, orgID)
	if err != nil {
		return 0, fmt.Errorf("clear gifts: %w", err)
	}
	return result.RowsAffected()
}

// GetGiftUnits returns the total gift units (SUM repeat_count) and the number
// of gift events recorded for one session. When no gift names are given (or only
// empty strings), all gifts count; otherwise only events whose gift_name matches
// one of the given names count.
//
// Scoped to the session: a goal belongs to the live it was created in, so its
// progress must not count gifts from other sessions of the same streamer.
func (db *DB) GetGiftUnits(ref model.LiveRef, giftNames ...string) (units, count int, err error) {
	if !ref.Valid() {
		return 0, 0, model.ErrInvalidID
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	query := "SELECT COALESCE(SUM(repeat_count), 0), COUNT(*) FROM gifts WHERE live_id = ?"
	args := []interface{}{ref.ID}
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
