package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// liveSessionReuseMaxAge bounds how long an open session stays resumable. A
// backend restart inside the same live resumes the open session; a session that
// stopped being touched for longer than this is treated as finished and the next
// start creates a new id.
const liveSessionReuseMaxAge = 10 * time.Hour

// liveSessionSelect is the column list shared by every session read.
const liveSessionSelect = `SELECT id, live_name, day, started_at, last_seen_at, ended_at FROM live_sessions`

// newLiveSessionID returns a random session id (16 bytes, hex).
func newLiveSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate live session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// scanLiveSession reads one live_sessions row.
func scanLiveSession(row interface{ Scan(...any) error }) (model.LiveSession, error) {
	var (
		s        model.LiveSession
		started  time.Time
		lastSeen time.Time
		ended    sql.NullTime
	)
	if err := row.Scan(&s.ID, &s.LiveName, &s.Day, &started, &lastSeen, &ended); err != nil {
		return model.LiveSession{}, err
	}
	s.Day = normalizeDate(s.Day)
	s.StartedAt = started.UTC().Format(time.RFC3339)
	s.LastSeenAt = lastSeen.UTC().Format(time.RFC3339)
	if ended.Valid {
		s.EndedAt = ended.Time.UTC().Format(time.RFC3339)
	}
	return s, nil
}

// BeginLiveSession opens the monitoring session for liveName.
//
// A session that is still open (ended_at IS NULL) and was touched on the same
// UTC day less than liveSessionReuseMaxAge ago is resumed, so a backend restart
// mid-live continues the same session instead of splitting one live in two.
// Otherwise a brand new id is created and any leftover open session of that
// streamer is closed with its real last_seen_at — that is what finally closes a
// live that ended without StopMonitoring. Nothing is ever deleted here.
func (db *DB) BeginLiveSession(liveName string, now time.Time) (model.LiveSession, error) {
	liveName = strings.TrimSpace(liveName)
	if liveName == "" {
		return model.LiveSession{}, fmt.Errorf("live name is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	day := now.Format("2006-01-02")

	db.mu.Lock()
	defer db.mu.Unlock()

	open, err := scanLiveSession(db.queryRow(
		liveSessionSelect+` WHERE live_name = ? AND ended_at IS NULL AND day = ?
		 ORDER BY started_at DESC LIMIT 1`,
		liveName, day,
	))
	switch {
	case err == nil:
		if lastSeen, perr := parseStoredTime(open.LastSeenAt); perr == nil && now.Sub(lastSeen) < liveSessionReuseMaxAge {
			if err := db.touchLiveSessionLocked(open.ID, now); err != nil {
				return model.LiveSession{}, err
			}
			return db.getLiveSessionLocked(open.ID)
		}
	case err != sql.ErrNoRows:
		return model.LiveSession{}, fmt.Errorf("lookup open live session: %w", err)
	}

	newID, err := newLiveSessionID()
	if err != nil {
		return model.LiveSession{}, err
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return model.LiveSession{}, fmt.Errorf("begin live session: %w", err)
	}
	// Rollback is a no-op once the transaction commits; its error is not
	// actionable, so it is intentionally ignored.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(db.bind(
		`UPDATE live_sessions SET ended_at = last_seen_at WHERE live_name = ? AND ended_at IS NULL`),
		liveName,
	); err != nil {
		return model.LiveSession{}, fmt.Errorf("close previous live sessions: %w", err)
	}
	if _, err := tx.Exec(db.bind(
		`INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`),
		newID, liveName, day, now, now,
	); err != nil {
		return model.LiveSession{}, fmt.Errorf("insert live session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.LiveSession{}, fmt.Errorf("commit live session: %w", err)
	}
	return db.getLiveSessionLocked(newID)
}

// EndLiveSession closes a session, keeping the first ended_at when called twice.
// Used by the shutdown path, which can reach both StopMonitoring and Close.
func (db *DB) EndLiveSession(id string, at time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.ErrInvalidID
	}
	if at.IsZero() {
		at = time.Now()
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, err := db.exec(
		`UPDATE live_sessions SET ended_at = COALESCE(ended_at, ?), last_seen_at = GREATEST(last_seen_at, ?) WHERE id = ?`,
		at.UTC(), at.UTC(), id,
	); err != nil {
		return fmt.Errorf("end live session: %w", err)
	}
	return nil
}

// TouchLiveSession refreshes last_seen_at, which is what the resume rule and the
// listing of open sessions rely on.
func (db *DB) TouchLiveSession(id string, at time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.ErrInvalidID
	}
	if at.IsZero() {
		at = time.Now()
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.touchLiveSessionLocked(id, at)
}

func (db *DB) touchLiveSessionLocked(id string, at time.Time) error {
	res, err := db.exec(
		`UPDATE live_sessions SET last_seen_at = ? WHERE id = ?`, at.UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("touch live session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("touch live session rows affected: %w", err)
	}
	if n == 0 {
		// The session was deleted (an admin deleted the live while it was still
		// streaming): the caller has to open a new one, otherwise subsequent
		// events would be written against a session that no longer exists.
		return model.ErrLiveSessionNotFound
	}
	return nil
}

// GetLiveSession returns one session by id.
func (db *DB) GetLiveSession(id string) (model.LiveSession, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.LiveSession{}, model.ErrInvalidID
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.getLiveSessionLocked(id)
}

func (db *DB) getLiveSessionLocked(id string) (model.LiveSession, error) {
	s, err := scanLiveSession(db.queryRow(liveSessionSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return model.LiveSession{}, model.ErrLiveSessionNotFound
	}
	if err != nil {
		return model.LiveSession{}, fmt.Errorf("query live session: %w", err)
	}
	return s, nil
}

// LatestLiveSession resolves the session of a streamer when the caller only has
// the name: the open session first, then the most recent one. Used for events
// that arrive without a liveId (the caller must not create a session here).
func (db *DB) LatestLiveSession(liveName string) (model.LiveSession, error) {
	liveName = strings.TrimSpace(liveName)
	if liveName == "" {
		return model.LiveSession{}, model.ErrLiveSessionNotFound
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	s, err := scanLiveSession(db.queryRow(
		liveSessionSelect+` WHERE live_name = ?
		 ORDER BY (ended_at IS NULL) DESC, started_at DESC LIMIT 1`,
		liveName,
	))
	if err == sql.ErrNoRows {
		return model.LiveSession{}, model.ErrLiveSessionNotFound
	}
	if err != nil {
		return model.LiveSession{}, fmt.Errorf("query latest live session: %w", err)
	}
	return s, nil
}

// parseStoredTime parses a stored timestamp string into UTC.
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

// ExecSQL runs a raw statement. Used by tests to seed timestamps.
func (db *DB) ExecSQL(query string, args ...any) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.exec(query, args...)
	return err
}
