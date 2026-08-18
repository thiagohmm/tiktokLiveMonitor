// Package database — implementation do model.Repository sobre Postgres
// (Supabase).
//
// Quando a variável de ambiente SUPABASE_DB_URL está definida, o Open()
// usa esta implementação em vez do SQLite local. As queries seguem a
// mesma semântica das versões SQLite; os placeholders "?" são convertidos
// para o estilo "$n" do driver pgx, os booleans saem como 0/1 (idêntico
// ao SQLite) e os timestamps são normalizados como strings RFC3339 UTC.
//
// O schema Postgres (idêntico ao criado em migrate()) está em
// supabase/schema.sql e deve ser aplicado no SQL Editor do projeto
// Supabase uma única vez.
package database

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

var qMarkRe = regexp.MustCompile(`\?`)

// pgQuery converts SQLite-style "?" placeholders to "$1", "$2", ...
func pgQuery(query string) string {
	i := 0
	return qMarkRe.ReplaceAllStringFunc(query, func(string) string {
		i++
		return fmt.Sprintf("$%d", i)
	})
}

// supTimestamp renders a UTC time using the same RFC3339 format the
// SQLite writers use, so downstream string consumers keep working.
func supTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// supScanTime adapts a timestamp value returned by Postgres
// (time.Time or string) into the RFC3339 string used by the models.
func supScanTime(v any) (string, error) {
	switch s := v.(type) {
	case time.Time:
		if s.IsZero() {
			return "", nil
		}
		return supTimestamp(s), nil
	case string:
		if s == "" {
			return "", nil
		}
		return s, nil
	default:
		return "", fmt.Errorf("unexpected timestamp value: %T", v)
	}
}

// --- FeedbackRepository ---

func (db *DB) addSupFeedback(comment, category, expected string) (int64, error) {
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
		pgQuery(`INSERT INTO false_positives (comment, category, expected) VALUES (?, ?, ?)`),
		comment, category, expected,
	)
	if err != nil {
		return 0, fmt.Errorf("insert feedback: %w", err)
	}
	return result.LastInsertId()
}

func (db *DB) getSupRecentFeedbacks(limit int) ([]model.Feedback, error) {
	if limit < 1 || limit > 200 {
		limit = 10
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		pgQuery(`SELECT comment, category, expected FROM false_positives ORDER BY timestamp DESC LIMIT ?`),
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

func (db *DB) logSupAnomaly(liveName, comment string, isAnomaly bool, category, uniqueID string) error {
	now := time.Now()
	day := now.UTC().Format("2006-01-02")
	var anomalyInt int
	if isAnomaly {
		anomalyInt = 1
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(
		pgQuery(`INSERT INTO anomaly_logs (live_name, day, uniqueId, comment, is_anomaly, category)
		 VALUES (?, ?, ?, ?, ?, ?)`),
		liveName, day, uniqueID, comment, anomalyInt, category,
	)
	if err != nil {
		return fmt.Errorf("insert anomaly: %w", err)
	}
	return nil
}

// supAnomalyLogRow scans a single anomaly_logs row from Postgres.
func supAnomalyLogRow(rows *sql.Rows) (*model.AnomalyLog, error) {
	var (
		a         model.AnomalyLog
		ts        any
		uniqueID  sql.NullString
		isAnomaly sql.NullInt64
		category  sql.NullString
	)
	if err := rows.Scan(&a.ID, &a.LiveName, &a.Day, &ts, &uniqueID, &a.Comment, &isAnomaly, &category); err != nil {
		return nil, err
	}
	s, err := supScanTime(ts)
	if err != nil {
		return nil, err
	}
	a.Timestamp = s
	a.UniqueID = uniqueID.String
	a.IsAnomaly = isAnomaly.Int64 != 0
	a.Category = category.String
	return &a, nil
}

func (db *DB) getSupRecentModerations(limit int) ([]model.AnomalyLog, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		pgQuery(`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs ORDER BY timestamp DESC LIMIT ?`),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query moderations: %w", err)
	}
	defer rows.Close()

	var out []model.AnomalyLog
	for rows.Next() {
		a, err := supAnomalyLogRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan anomaly: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (db *DB) getSupRecentAnomalyLogs(limit int) ([]model.AnomalyLog, error) {
	return db.getSupRecentModerations(limit)
}

func (db *DB) getSupAnomalyLogsByLiveName(liveName string) ([]model.AnomalyLog, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		pgQuery(`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category FROM anomaly_logs WHERE live_name = ?`),
		liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query anomaly logs: %w", err)
	}
	defer rows.Close()

	var out []model.AnomalyLog
	for rows.Next() {
		a, err := supAnomalyLogRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan anomaly log: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (db *DB) clearSupHistory() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(`DELETE FROM anomaly_logs`)
	if err != nil {
		return 0, fmt.Errorf("clear history: %w", err)
	}
	return result.RowsAffected()
}

func (db *DB) deleteSupModeration(id int64) (int64, error) {
	if id <= 0 {
		return 0, model.ErrInvalidID
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(pgQuery(`DELETE FROM anomaly_logs WHERE id = ?`), id)
	if err != nil {
		return 0, fmt.Errorf("delete moderation: %w", err)
	}
	return result.RowsAffected()
}

func (db *DB) cleanupSupOldAnomalies() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(`DELETE FROM anomaly_logs WHERE day < now()::date`)
	if err != nil {
		return 0, fmt.Errorf("cleanup old anomalies: %w", err)
	}
	return result.RowsAffected()
}

// --- UserMessageRepository ---

func (db *DB) addSupUserMessageDedup(uniqueID, username, message string) error {
	if message == "" || uniqueID == "" {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	var count int
	err := db.conn.QueryRow(
		pgQuery(`SELECT COUNT(*) FROM user_messages WHERE LOWER(uniqueId) = ? AND LOWER(message) = ?`),
		uniqueID, message,
	).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	_, err = db.conn.Exec(
		pgQuery(`INSERT INTO user_messages (uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?)`),
		uniqueID, username, message, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert user message: %w", err)
	}

	// Keep only the 10 most recent unique messages per user.
	_, err = db.conn.Exec(pgQuery(`
		DELETE FROM user_messages WHERE id NOT IN (
			SELECT id FROM user_messages WHERE LOWER(uniqueId) = ?
			ORDER BY timestamp DESC LIMIT 10
		) AND LOWER(uniqueId) = ?
	`), uniqueID, uniqueID)
	if err != nil {
		return fmt.Errorf("prune user messages: %w", err)
	}
	return nil
}

// supUserMessageRow scans a single user_messages row (id optional).
func supUserMessageRow(withID bool, rows *sql.Rows) (*model.UserMessage, error) {
	var (
		um model.UserMessage
		ts any
	)
	if withID {
		if err := rows.Scan(&um.ID, &um.UniqueID, &um.Username, &um.Message, &ts); err != nil {
			return nil, err
		}
	} else {
		if err := rows.Scan(&um.UniqueID, &um.Username, &um.Message, &ts); err != nil {
			return nil, err
		}
	}
	s, err := supScanTime(ts)
	if err != nil {
		return nil, err
	}
	um.Timestamp = s
	return &um, nil
}

func (db *DB) getSupUserMessages(uniqueID string) ([]model.UserMessage, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return nil, model.ErrUniqueIDRequired
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		pgQuery(`SELECT id, uniqueId, username, message, timestamp FROM user_messages WHERE LOWER(uniqueId) = ? ORDER BY timestamp DESC`),
		uniqueID,
	)
	if err != nil {
		return nil, fmt.Errorf("query user messages: %w", err)
	}
	defer rows.Close()

	var out []model.UserMessage
	for rows.Next() {
		um, err := supUserMessageRow(true, rows)
		if err != nil {
			return nil, fmt.Errorf("scan user message: %w", err)
		}
		out = append(out, *um)
	}
	return out, rows.Err()
}

func (db *DB) getAllSupUserMessages() (map[string][]model.UserMessage, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		`SELECT uniqueId, username, message, timestamp FROM user_messages ORDER BY uniqueId, timestamp DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query all user messages: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]model.UserMessage)
	for rows.Next() {
		um, err := supUserMessageRow(false, rows)
		if err != nil {
			return nil, fmt.Errorf("scan user message: %w", err)
		}
		result[um.UniqueID] = append(result[um.UniqueID], *um)
	}
	return result, rows.Err()
}

func (db *DB) getSupTodayUserMessages() ([]model.UserMessage, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		`SELECT id, uniqueId, username, message, timestamp
		 FROM user_messages
		 WHERE (timestamp AT TIME ZONE 'UTC')::date = now() AT TIME ZONE 'UTC'
		 ORDER BY timestamp ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query today user messages: %w", err)
	}
	defer rows.Close()

	var out []model.UserMessage
	for rows.Next() {
		um, err := supUserMessageRow(true, rows)
		if err != nil {
			return nil, fmt.Errorf("scan today user message: %w", err)
		}
		out = append(out, *um)
	}
	return out, rows.Err()
}

func (db *DB) getSupTodayAnomalyLogs(liveName string) ([]model.AnomalyLog, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		pgQuery(`SELECT id, live_name, day, timestamp, uniqueId, comment, is_anomaly, category
		 FROM anomaly_logs
		 WHERE day = now()::date AND live_name = ?
		 ORDER BY timestamp ASC`),
		liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query today anomaly logs: %w", err)
	}
	defer rows.Close()

	var out []model.AnomalyLog
	for rows.Next() {
		a, err := supAnomalyLogRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan today anomaly log: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// --- GiftRepository ---

func (db *DB) addSupGift(liveName, uniqueID, nickname, giftName string, repeatCount, giftType int) (int64, error) {
	if repeatCount < 1 {
		repeatCount = 1
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(
		pgQuery(`INSERT INTO gifts (live_name, uniqueId, nickname, gift_name, repeat_count, gift_type) VALUES (?, ?, ?, ?, ?, ?)`),
		liveName, uniqueID, nickname, giftName, repeatCount, giftType,
	)
	if err != nil {
		return 0, fmt.Errorf("insert gift: %w", err)
	}
	return result.LastInsertId()
}

// supGiftRow scans a single gifts row from Postgres.
func supGiftRow(rows *sql.Rows) (*model.Gift, error) {
	var (
		g model.Gift
		ts any
	)
	if err := rows.Scan(&g.ID, &g.LiveName, &g.UniqueID, &g.Nickname, &g.GiftName, &g.RepeatCount, &g.GiftType, &ts); err != nil {
		return nil, err
	}
	s, err := supScanTime(ts)
	if err != nil {
		return nil, err
	}
	g.Timestamp = s
	return &g, nil
}

func (db *DB) getSupRecentGifts(liveName string, limit int) ([]model.Gift, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		pgQuery(`SELECT id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type, timestamp FROM gifts WHERE live_name = ? ORDER BY timestamp DESC LIMIT ?`),
		liveName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query gifts: %w", err)
	}
	defer rows.Close()

	var out []model.Gift
	for rows.Next() {
		g, err := supGiftRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan gift: %w", err)
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (db *DB) getSupGiftsByUser(uniqueID string) ([]model.Gift, error) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	if uniqueID == "" {
		return nil, model.ErrUniqueIDRequired
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		pgQuery(`SELECT id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type, timestamp FROM gifts WHERE LOWER(uniqueId) = ? ORDER BY timestamp DESC`),
		uniqueID,
	)
	if err != nil {
		return nil, fmt.Errorf("query gifts by user: %w", err)
	}
	defer rows.Close()

	var out []model.Gift
	for rows.Next() {
		g, err := supGiftRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan gift: %w", err)
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (db *DB) getSupGiftSummary() (map[string]map[string]int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.conn.Query(
		`SELECT uniqueId, nickname, gift_name, SUM(repeat_count) AS total FROM gifts GROUP BY uniqueId, nickname, gift_name ORDER BY total DESC`,
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

func (db *DB) clearSupGifts() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(`DELETE FROM gifts`)
	if err != nil {
		return 0, fmt.Errorf("clear gifts: %w", err)
	}
	return result.RowsAffected()
}

// --- TargetGiftHistoryRepository ---

func (db *DB) addSupTargetGiftHistory(liveName, uniqueID, nickname, giftName string, receivedAt time.Time) (int64, error) {
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec(
		pgQuery(`INSERT INTO target_gift_history
			(live_name, uniqueId, nickname, gift_name, received_at)
		 VALUES (?, ?, ?, ?, ?)`),
		liveName, uniqueID, nickname, giftName, receivedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("insert target gift history: %w", err)
	}
	return result.LastInsertId()
}

func (db *DB) markSupTargetGiftAnswered(id int64, responseType string, answeredAt time.Time) error {
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
		pgQuery(`UPDATE target_gift_history
		 SET answered_at = ?, response_type = ?
		 WHERE id = ? AND answered_at IS NULL`),
		answeredAt, responseType, id,
	)
	if err != nil {
		return fmt.Errorf("mark target gift answered: %w", err)
	}
	return nil
}

func (db *DB) getSupRecentTargetGiftHistory(liveName string, limit int) ([]model.TargetGiftHistory, error) {
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
			pgQuery(`SELECT id, live_name, uniqueId, nickname, gift_name, received_at, answered_at, response_type
			 FROM target_gift_history
			 ORDER BY received_at DESC
			 LIMIT ?`),
			limit,
		)
	} else {
		rows, err = db.conn.Query(
			pgQuery(`SELECT id, live_name, uniqueId, nickname, gift_name, received_at, answered_at, response_type
			 FROM target_gift_history
			 WHERE live_name = ?
			 ORDER BY received_at DESC
			 LIMIT ?`),
			liveName, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query target gift history: %w", err)
	}
	defer rows.Close()

	var out []model.TargetGiftHistory
	for rows.Next() {
		var (
			h            model.TargetGiftHistory
			answeredAt   any
			respType     sql.NullString
		)
		if err := rows.Scan(
			&h.ID, &h.LiveName, &h.UniqueID, &h.Nickname, &h.GiftName,
			&h.ReceivedAt, &answeredAt, &respType,
		); err != nil {
			return nil, fmt.Errorf("scan target gift history: %w", err)
		}
		if s, err := supScanTime(answeredAt); err != nil {
			return nil, fmt.Errorf("scan target gift answered_at: %w", err)
		} else if s != "" {
			v := s
			h.AnsweredAt = &v
		}
		if respType.Valid {
			v := respType.String
			h.ResponseType = &v
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// --- SessionRepository ---

func (db *DB) supMaxTimestamp(query string, args ...any) (time.Time, bool, error) {
	var raw sql.NullTime
	if err := db.conn.QueryRow(query, args...).Scan(&raw); err != nil {
		return time.Time{}, false, err
	}
	if !raw.Valid {
		return time.Time{}, false, nil
	}
	return raw.Time, true, nil
}

func (db *DB) supGetLastSessionActivity(liveName string) (time.Time, bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	queries := []struct {
		q    string
		args []any
	}{
		{"SELECT MAX(timestamp) FROM gifts WHERE live_name = ?", []any{liveName}},
		{"SELECT MAX(timestamp) FROM anomaly_logs WHERE live_name = ?", []any{liveName}},
		{"SELECT MAX(timestamp) FROM user_messages", nil},
	}

	var latest time.Time
	found := false
	for _, q := range queries {
		ts, ok, err := db.supMaxTimestamp(pgQuery(q.q), q.args...)
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

func (db *DB) supDeleteSessionData(liveName string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(pgQuery(`DELETE FROM gifts WHERE live_name = ?`), liveName); err != nil {
		return fmt.Errorf("delete session gifts: %w", err)
	}
	if _, err := tx.Exec(pgQuery(`DELETE FROM anomaly_logs WHERE live_name = ?`), liveName); err != nil {
		return fmt.Errorf("delete session anomalies: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM user_messages`); err != nil {
		return fmt.Errorf("delete session messages: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	return nil
}

// --- Test helper ---

// ExecSupSQL runs a raw statement on the Postgres backend (test-only).
func (db *DB) ExecSupSQL(query string, args ...any) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec(pgQuery(query), args...)
	return err
}
