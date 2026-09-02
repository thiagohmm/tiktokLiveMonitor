package database

import (
	"database/sql"
	"errors"
	"fmt"
)

// GetSetting returns the stored value for a settings key. An empty string and
// nil error are returned when the key does not exist yet.
func (db *DB) GetSetting(key string) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var value string
	err := db.queryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get setting %s: %w", key, err)
	}
	return value, nil
}

// SetSetting upserts a settings key/value pair.
func (db *DB) SetSetting(key, value string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) "+
			"ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}
