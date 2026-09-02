package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"strings"
	"time"
)

// AddGiftGoal stores a new gift goal and returns its id.
func (db *DB) AddGiftGoal(g model.GiftGoal) (int64, error) {
	if strings.TrimSpace(g.Title) == "" {
		return 0, fmt.Errorf("goal title is required")
	}
	if g.TargetUnits < 1 {
		return 0, fmt.Errorf("target units must be >= 1")
	}
	if strings.TrimSpace(g.LiveName) == "" {
		return 0, fmt.Errorf("live name is required")
	}
	if g.Status == "" {
		g.Status = model.GoalStatusActive
	}
	if g.Milestones == nil {
		g.Milestones = []model.GoalMilestone{}
	}
	if g.CreatedAt == "" {
		g.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	milestonesJSON, err := json.Marshal(g.Milestones)
	if err != nil {
		return 0, fmt.Errorf("marshal goal milestones: %w", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	id, err := db.insertID(
		`INSERT INTO gift_goals (live_name, title, gift_name, target_units, status, milestones, created_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		g.LiveName, g.Title, g.GiftName, g.TargetUnits, g.Status, string(milestonesJSON),
		g.CreatedAt, nullTime(g.CompletedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert gift goal: %w", err)
	}
	return id, nil
}

// GetGiftGoals returns all goals for a live, newest first.
func (db *DB) GetGiftGoals(liveName string) ([]model.GiftGoal, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT id, live_name, title, gift_name, target_units, status, milestones, created_at, completed_at
		 FROM gift_goals
		 WHERE live_name = ?
		 ORDER BY id DESC`,
		liveName,
	)
	if err != nil {
		return nil, fmt.Errorf("query gift goals: %w", err)
	}
	defer closeRows(rows)

	out := make([]model.GiftGoal, 0)
	for rows.Next() {
		g, err := scanGiftGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveGiftGoal persists an existing goal's mutable fields (title, target,
// status, milestones, completed_at).
func (db *DB) SaveGiftGoal(g model.GiftGoal) error {
	if g.ID <= 0 {
		return model.ErrInvalidID
	}
	milestonesJSON, err := json.Marshal(g.Milestones)
	if err != nil {
		return fmt.Errorf("marshal goal milestones: %w", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err = db.exec(
		`UPDATE gift_goals
		 SET title = ?, gift_name = ?, target_units = ?, status = ?, milestones = ?, completed_at = ?
		 WHERE id = ?`,
		g.Title, g.GiftName, g.TargetUnits, g.Status, string(milestonesJSON), nullTime(g.CompletedAt), g.ID,
	)
	if err != nil {
		return fmt.Errorf("save gift goal: %w", err)
	}
	return nil
}

// DeleteGiftGoals removes all goals for a live and returns the rows deleted.
func (db *DB) DeleteGiftGoals(liveName string) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.exec("DELETE FROM gift_goals WHERE live_name = ?", liveName)
	if err != nil {
		return 0, fmt.Errorf("delete gift goals: %w", err)
	}
	return result.RowsAffected()
}

func scanGiftGoal(rows *sql.Rows) (model.GiftGoal, error) {
	var (
		g           model.GiftGoal
		milestones  string
		completedAt sql.NullString
	)
	if err := rows.Scan(&g.ID, &g.LiveName, &g.Title, &g.GiftName, &g.TargetUnits, &g.Status, &milestones, &g.CreatedAt, &completedAt); err != nil {
		return model.GiftGoal{}, fmt.Errorf("scan gift goal: %w", err)
	}
	if err := json.Unmarshal([]byte(milestones), &g.Milestones); err != nil {
		return model.GiftGoal{}, fmt.Errorf("unmarshal goal milestones: %w", err)
	}
	if g.Milestones == nil {
		g.Milestones = []model.GoalMilestone{}
	}
	if completedAt.Valid {
		v := normalizeTime(completedAt.String)
		g.CompletedAt = &v
	}
	g.CreatedAt = normalizeTime(g.CreatedAt)
	return g, nil
}

// nullTime converts a pointer timestamp to a nullable argument (nil stays NULL).
func nullTime(p *string) interface{} {
	if p == nil {
		return nil
	}
	v := *p
	return v
}
