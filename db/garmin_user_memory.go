package db

import (
	"database/sql"
	"errors"
	"time"
)

// GarminUserMemory is a user's explicitly saved, durable profile for Garmin AI.
// It is separate from the short-lived conversation history and global admin memory.
type GarminUserMemory struct {
	Info      string
	Pronouns  string
	Bio       string
	UpdatedAt time.Time
}

func (m GarminUserMemory) Empty() bool {
	return m.Info == "" && m.Pronouns == "" && m.Bio == ""
}

func (d *DB) GetGarminUserMemory(platform, userID string) (GarminUserMemory, error) {
	var memory GarminUserMemory
	var updatedAt int64
	err := d.conn.QueryRow(`
		SELECT info, pronouns, bio, updated_at
		FROM garmin_user_memory
		WHERE platform = ? AND user_id = ?
	`, platform, userID).Scan(&memory.Info, &memory.Pronouns, &memory.Bio, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return GarminUserMemory{}, nil
	}
	if err != nil {
		return GarminUserMemory{}, err
	}
	memory.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return memory, nil
}

func (d *DB) SetGarminUserMemory(platform, userID string, memory GarminUserMemory) error {
	_, err := d.conn.Exec(`
		INSERT INTO garmin_user_memory (platform, user_id, info, pronouns, bio, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(platform, user_id) DO UPDATE SET
			info = excluded.info,
			pronouns = excluded.pronouns,
			bio = excluded.bio,
			updated_at = excluded.updated_at
	`, platform, userID, memory.Info, memory.Pronouns, memory.Bio, time.Now().UTC().Unix())
	return err
}

func (d *DB) DeleteGarminUserMemory(platform, userID string) error {
	_, err := d.conn.Exec(
		"DELETE FROM garmin_user_memory WHERE platform = ? AND user_id = ?",
		platform, userID,
	)
	return err
}
