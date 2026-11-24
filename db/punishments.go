package db

import "fmt"

// punishmentTables limits the table names interpolated into SQL below.
var punishmentTables = map[string]bool{"timed_bans": true, "mutes": true}

// isCurrentPunishment reports whether the row still exists and no newer row
// exists for the same target. Only the current row may lift the punishment.
func (d *DB) isCurrentPunishment(table string, id int64) (bool, error) {
	if !punishmentTables[table] {
		return false, fmt.Errorf("unknown punishment table %q", table)
	}
	var current bool
	err := d.conn.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM `+table+` p WHERE p.id = ? AND NOT EXISTS (
			SELECT 1 FROM `+table+` n
			WHERE n.platform = p.platform AND n.chat_id = p.chat_id AND n.user_id = p.user_id AND n.id > p.id
		)
	)`, id).Scan(&current)
	return current, err
}

// deleteOtherPunishments removes every row for the target except keepID.
// Pass keepID 0 to remove all rows.
func (d *DB) deleteOtherPunishments(table, platform, chatID, userID string, keepID int64) error {
	if !punishmentTables[table] {
		return fmt.Errorf("unknown punishment table %q", table)
	}
	_, err := d.conn.Exec("DELETE FROM "+table+" WHERE platform = ? AND chat_id = ? AND user_id = ? AND id != ?", platform, chatID, userID, keepID)
	return err
}

// IsCurrentTimedBan reports whether the timed ban still owns the target's ban.
func (d *DB) IsCurrentTimedBan(id int64) (bool, error) {
	return d.isCurrentPunishment("timed_bans", id)
}

// DeleteOtherTimedBans removes the target's timed bans except keepID (0 removes all).
func (d *DB) DeleteOtherTimedBans(platform, chatID, userID string, keepID int64) error {
	return d.deleteOtherPunishments("timed_bans", platform, chatID, userID, keepID)
}

// IsCurrentMute reports whether the mute still owns the target's restriction.
func (d *DB) IsCurrentMute(id int64) (bool, error) {
	return d.isCurrentPunishment("mutes", id)
}

// DeleteOtherMutes removes the target's mutes except keepID (0 removes all).
func (d *DB) DeleteOtherMutes(platform, chatID, userID string, keepID int64) error {
	return d.deleteOtherPunishments("mutes", platform, chatID, userID, keepID)
}

// PendingMuteExpiry returns the latest stored mute expiry for the target, or 0.
func (d *DB) PendingMuteExpiry(platform, chatID, userID string) (int64, error) {
	var expiresAt int64
	err := d.conn.QueryRow("SELECT COALESCE(MAX(expires_at), 0) FROM mutes WHERE platform = ? AND chat_id = ? AND user_id = ?", platform, chatID, userID).Scan(&expiresAt)
	return expiresAt, err
}
