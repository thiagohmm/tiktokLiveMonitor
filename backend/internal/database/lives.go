package database

import (
	"database/sql"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"strings"
	"time"
)

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
	defer closeRows(rows)

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
	defer closeRows(msgRows)
	for msgRows.Next() {
		var uid, uname, first, last sql.NullString
		var n, questions int
		if err := msgRows.Scan(&uid, &uname, &n, &questions, &first, &last); err != nil {
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

	// Gifts. Grouped by (user, gift) so each gift's researched coin value can
	// be applied before summing the user's total gift value.
	giftRows, err := db.query(`
		SELECT uniqueId, COALESCE((array_agg(nickname ORDER BY timestamp DESC))[1], '') AS nickname, gift_name, COUNT(*) AS n, SUM(repeat_count) AS total,
			MIN(timestamp) AS first, MAX(timestamp) AS last
		FROM gifts WHERE live_name = ? GROUP BY uniqueId, gift_name`, liveName)
	if err != nil {
		return nil, fmt.Errorf("query live stats gifts: %w", err)
	}
	defer closeRows(giftRows)
	for giftRows.Next() {
		var uid, uname, giftName string
		var n, total int
		var first, last sql.NullString
		if err := giftRows.Scan(&uid, &uname, &giftName, &n, &total, &first, &last); err != nil {
			return nil, fmt.Errorf("scan live stat gift: %w", err)
		}
		if uid == "" {
			continue
		}
		s := stats[uid]
		s.UniqueID = uid
		// Keep the nickname from the most recent gift group (same heuristic as
		// the per-group array_agg ... ORDER BY timestamp DESC pick).
		if last.Valid && last.String != "" && last.String > s.LastSeen {
			s.Nickname = coalesceStr(uname, uid)
		}
		s.GiftCount += n
		s.GiftTotal += total
		s.GiftValue += total * model.GiftValue(giftName)
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

	// Shares.
	shareRows, err := db.query(`
		SELECT uniqueId, COALESCE((array_agg(nickname ORDER BY timestamp DESC))[1], '') AS nickname, COUNT(*) AS n,
			MIN(timestamp) AS first, MAX(timestamp) AS last
		FROM shares WHERE live_name = ? GROUP BY uniqueId`, liveName)
	if err != nil {
		return nil, fmt.Errorf("query live stats shares: %w", err)
	}
	defer closeRows(shareRows)
	for shareRows.Next() {
		var uid, uname string
		var n int
		var first, last sql.NullString
		if err := shareRows.Scan(&uid, &uname, &n, &first, &last); err != nil {
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

	// Likes (hearts).
	likeRows, err := db.query(`
		SELECT uniqueId, COALESCE((array_agg(nickname ORDER BY timestamp DESC))[1], '') AS nickname, SUM(like_count) AS n,
			MIN(timestamp) AS first, MAX(timestamp) AS last
		FROM likes WHERE live_name = ? GROUP BY uniqueId`, liveName)
	if err != nil {
		return nil, fmt.Errorf("query live stats likes: %w", err)
	}
	defer closeRows(likeRows)
	for likeRows.Next() {
		var uid, uname string
		var n int
		var first, last sql.NullString
		if err := likeRows.Scan(&uid, &uname, &n, &first, &last); err != nil {
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
	defer closeRows(rows)
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
