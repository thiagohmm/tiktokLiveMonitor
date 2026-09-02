package database

import (
	"database/sql"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"strings"
	"time"
)

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
	defer closeRows(rows)

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
