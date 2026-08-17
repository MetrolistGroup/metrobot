package db

import (
	"database/sql"
	"errors"
	"time"
)

func (d *DB) SetGarminContextCutoff(channelID, messageID string) error {
	_, err := d.conn.Exec(`
		INSERT INTO garmin_context_cutoffs (channel_id, message_id, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			message_id = excluded.message_id,
			updated_at = excluded.updated_at
	`, channelID, messageID, time.Now().Unix())
	return err
}

func (d *DB) GetGarminContextCutoff(channelID string) (string, error) {
	var messageID string
	err := d.conn.QueryRow(`SELECT message_id FROM garmin_context_cutoffs WHERE channel_id = ?`, channelID).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return messageID, err
}
