package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	garminUserMemoryInfoLimit     = 4000
	garminUserMemoryPronounsLimit = 100
	garminUserMemoryBioLimit      = 500
)

// GarminUserMemory is a user's durable Garmin AI profile. It is separate from
// short-lived conversation history and global admin memory.
type GarminUserMemory struct {
	Info      string
	Pronouns  string
	Bio       string
	UpdatedAt time.Time
}

type GarminUserMemoryEntry struct {
	UserID string
	GarminUserMemory
}

type GarminMemoryConsent struct {
	Decided bool
	Enabled bool
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

func (d *DB) ListGarminUserMemories(platform string) ([]GarminUserMemoryEntry, error) {
	rows, err := d.conn.Query(`
		SELECT user_id, info, pronouns, bio, updated_at
		FROM garmin_user_memory
		WHERE platform = ?
		ORDER BY updated_at DESC, user_id
	`, platform)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []GarminUserMemoryEntry
	for rows.Next() {
		var entry GarminUserMemoryEntry
		var updatedAt int64
		if err := rows.Scan(&entry.UserID, &entry.Info, &entry.Pronouns, &entry.Bio, &updatedAt); err != nil {
			return nil, err
		}
		entry.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (d *DB) GetGarminMemoryConsent(platform, userID string) (GarminMemoryConsent, error) {
	var enabled bool
	err := d.conn.QueryRow(`
		SELECT memory_enabled
		FROM garmin_memory_consent
		WHERE platform = ? AND user_id = ?
	`, platform, userID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return GarminMemoryConsent{}, nil
	}
	if err != nil {
		return GarminMemoryConsent{}, err
	}
	return GarminMemoryConsent{Decided: true, Enabled: enabled}, nil
}

func (d *DB) SetGarminMemoryConsent(platform, userID string, enabled bool) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO garmin_memory_consent (platform, user_id, memory_enabled, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, user_id) DO UPDATE SET
			memory_enabled = excluded.memory_enabled,
			updated_at = excluded.updated_at
	`, platform, userID, enabled, time.Now().UTC().Unix()); err != nil {
		return err
	}
	if !enabled {
		if _, err := tx.Exec(
			"DELETE FROM garmin_user_memory WHERE platform = ? AND user_id = ?",
			platform, userID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) SetGarminUserMemory(platform, userID string, memory GarminUserMemory) error {
	if len([]rune(memory.Info)) > garminUserMemoryInfoLimit ||
		len([]rune(memory.Pronouns)) > garminUserMemoryPronounsLimit ||
		len([]rune(memory.Bio)) > garminUserMemoryBioLimit {
		return fmt.Errorf("garmin user memory exceeds profile field limits")
	}
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
