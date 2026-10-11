package db

import (
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

const GarminPromptMaxSize = 64 * 1024
const garminPromptKey = "garmin_base_system_prompt"

var ErrInvalidGarminPrompt = errors.New("prompt must be nonblank valid UTF-8 and at most 64 KiB")

func (d *DB) migrateSettings() error {
	_, err := d.conn.Exec(`CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)
	return err
}

func (d *DB) GetGarminSystemPrompt() (string, bool, error) {
	var content string
	err := d.conn.QueryRow(`SELECT value FROM settings WHERE key = ?`, garminPromptKey).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return content, err == nil, err
}

// SetGarminSystemPrompt preserves content exactly; SQLite commits the upsert atomically.
func (d *DB) SetGarminSystemPrompt(content string) error {
	if strings.TrimSpace(content) == "" || !utf8.ValidString(content) || len(content) > GarminPromptMaxSize {
		return ErrInvalidGarminPrompt
	}
	_, err := d.conn.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, garminPromptKey, content)
	return err
}

func (d *DB) ResetGarminSystemPrompt() error {
	_, err := d.conn.Exec(`DELETE FROM settings WHERE key = ?`, garminPromptKey)
	return err
}
