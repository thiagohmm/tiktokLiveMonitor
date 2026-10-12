package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// AddMinuteMetrics adds counts to one UTC minute of a live. A second call for
// the same minute accumulates. A conflicting row of another organization is
// left unchanged.
func (db *DB) AddMinuteMetrics(orgID, liveID string, bucket time.Time, messages, likes, shares, giftUnits, giftDiamonds int) error {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return err
	}
	liveID = strings.TrimSpace(liveID)
	if liveID == "" {
		return model.ErrInvalidID
	}
	bucket = bucket.UTC().Truncate(time.Minute)
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err = db.exec(
		`INSERT INTO live_minute_metrics
			(org_id, live_id, bucket, messages, likes, shares, gift_units, gift_diamonds)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (live_id, bucket) DO UPDATE SET
			messages = live_minute_metrics.messages + EXCLUDED.messages,
			likes = live_minute_metrics.likes + EXCLUDED.likes,
			shares = live_minute_metrics.shares + EXCLUDED.shares,
			gift_units = live_minute_metrics.gift_units + EXCLUDED.gift_units,
			gift_diamonds = live_minute_metrics.gift_diamonds + EXCLUDED.gift_diamonds
		 WHERE live_minute_metrics.org_id = EXCLUDED.org_id`,
		orgID, liveID, bucket, messages, likes, shares, giftUnits, giftDiamonds,
	)
	if err != nil {
		return fmt.Errorf("upsert minute metrics: %w", err)
	}
	return nil
}

// CountMinuteBuckets counts stored minutes of one live inside the organization.
func (db *DB) CountMinuteBuckets(orgID, liveID string) (int, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return 0, err
	}
	liveID = strings.TrimSpace(liveID)
	if liveID == "" {
		return 0, model.ErrInvalidID
	}
	var n int
	if err := db.queryRow(
		`SELECT COUNT(*) FROM live_minute_metrics WHERE org_id = ? AND live_id = ?`,
		orgID, liveID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count minute buckets: %w", err)
	}
	return n, nil
}

// OpenLiveSession returns the open session of one streamer in the organization.
// The boolean is false when none is open.
func (db *DB) OpenLiveSession(orgID, liveName string) (model.LiveSession, bool, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return model.LiveSession{}, false, err
	}
	liveName = strings.TrimSpace(liveName)
	if liveName == "" {
		return model.LiveSession{}, false, nil
	}
	session, err := scanLiveSession(db.queryRow(
		liveSessionSelect+` WHERE org_id = ? AND live_name = ? AND ended_at IS NULL
			ORDER BY started_at DESC LIMIT 1`,
		orgID, liveName,
	))
	if err == sql.ErrNoRows {
		return model.LiveSession{}, false, nil
	}
	if err != nil {
		return model.LiveSession{}, false, fmt.Errorf("open live session: %w", err)
	}
	return session, true, nil
}

// ReplaceForecast upserts the two horizons of one live. Rows of another
// organization that happen to share the session id are not overwritten.
func (db *DB) ReplaceForecast(orgID, liveID, version string, computed time.Time, horizons []model.ForecastHorizon) error {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return err
	}
	liveID = strings.TrimSpace(liveID)
	if liveID == "" {
		return model.ErrInvalidID
	}
	if computed.IsZero() {
		computed = time.Now().UTC()
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin forecast: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, horizon := range horizons {
		if horizon.Minutes != 5 && horizon.Minutes != 10 {
			return fmt.Errorf("unsupported forecast horizon %d", horizon.Minutes)
		}
		if _, err := tx.Exec(db.bind(
			`INSERT INTO live_forecasts
				(org_id, live_id, horizon_min, messages, likes, shares, gift_units, gift_diamonds, model_version, computed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (live_id, horizon_min) DO UPDATE SET
				messages = EXCLUDED.messages,
				likes = EXCLUDED.likes,
				shares = EXCLUDED.shares,
				gift_units = EXCLUDED.gift_units,
				gift_diamonds = EXCLUDED.gift_diamonds,
				model_version = EXCLUDED.model_version,
				computed_at = EXCLUDED.computed_at
			 WHERE live_forecasts.org_id = EXCLUDED.org_id`),
			orgID, liveID, horizon.Minutes, horizon.Messages, horizon.Likes, horizon.Shares,
			horizon.GiftUnits, horizon.GiftDiamonds, version, computed.UTC(),
		); err != nil {
			return fmt.Errorf("upsert forecast: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit forecast: %w", err)
	}
	return nil
}

// ReadForecast loads the stored horizons of one live. An empty slice means
// the organization has no forecast for that session.
func (db *DB) ReadForecast(orgID, liveID string) (string, time.Time, []model.ForecastHorizon, error) {
	orgID, err := requireOrg(orgID)
	if err != nil {
		return "", time.Time{}, nil, err
	}
	liveID = strings.TrimSpace(liveID)
	if liveID == "" {
		return "", time.Time{}, nil, model.ErrInvalidID
	}
	rows, err := db.query(
		`SELECT horizon_min, messages, likes, shares, gift_units, gift_diamonds, model_version, computed_at
		 FROM live_forecasts WHERE org_id = ? AND live_id = ? ORDER BY horizon_min`,
		orgID, liveID,
	)
	if err != nil {
		return "", time.Time{}, nil, fmt.Errorf("read forecast: %w", err)
	}
	defer closeRows(rows)
	var (
		version  string
		computed time.Time
		out      []model.ForecastHorizon
	)
	for rows.Next() {
		var (
			horizon model.ForecastHorizon
			at      time.Time
		)
		if err := rows.Scan(
			&horizon.Minutes, &horizon.Messages, &horizon.Likes, &horizon.Shares,
			&horizon.GiftUnits, &horizon.GiftDiamonds, &version, &at,
		); err != nil {
			return "", time.Time{}, nil, fmt.Errorf("scan forecast: %w", err)
		}
		if at.After(computed) {
			computed = at
		}
		out = append(out, horizon)
	}
	if err := rows.Err(); err != nil {
		return "", time.Time{}, nil, fmt.Errorf("forecast rows: %w", err)
	}
	return version, computed, out, nil
}
