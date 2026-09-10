package database

import (
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"strings"
)

// AddShare stores a share of the live made by a participant.
func (db *DB) AddShare(ref model.LiveRef, uniqueID, nickname string) error {
	if !ref.Valid() {
		return model.ErrInvalidID
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, err := db.exec(
		"INSERT INTO shares (live_id, live_name, uniqueId, nickname) VALUES (?, ?, ?, ?)",
		ref.ID, strings.TrimSpace(ref.Name), uniqueID, nickname,
	); err != nil {
		return fmt.Errorf("insert share: %w", err)
	}
	return nil
}

// AddLike stores a batch of likes (hearts) sent by a participant during a live.
// likeCount is the number of likes in this event (usually 1, but events can carry
// a burst). The per-user total is obtained by SUM(like_count).
func (db *DB) AddLike(ref model.LiveRef, uniqueID, nickname string, likeCount int) error {
	if !ref.Valid() {
		return model.ErrInvalidID
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	if likeCount < 1 {
		likeCount = 1
	}

	if _, err := db.exec(
		"INSERT INTO likes (live_id, live_name, uniqueId, nickname, like_count) VALUES (?, ?, ?, ?, ?)",
		ref.ID, strings.TrimSpace(ref.Name), uniqueID, nickname, likeCount,
	); err != nil {
		return fmt.Errorf("insert like: %w", err)
	}
	return nil
}

// UpsertRoomLikeTotal stores the room-level cumulative like counter reported
// by the stream. The stream value is monotonic per live, so only the highest
// value seen is kept. Stored per session so deleting a live removes its counter.
func (db *DB) UpsertRoomLikeTotal(ref model.LiveRef, total int64) error {
	if !ref.Valid() {
		return model.ErrInvalidID
	}
	if total <= 0 {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.upsertRoomLikeTotal(ref, total); err != nil {
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
