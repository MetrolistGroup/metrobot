package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("setting journal mode: %w", err)
	}
	if _, err := conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return d, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS notes (
			name    TEXT PRIMARY KEY,
			content TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admins (
			platform   TEXT NOT NULL,
			user_id    TEXT NOT NULL,
			added_by   TEXT NOT NULL,
			PRIMARY KEY (platform, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS timed_bans (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			platform   TEXT NOT NULL,
			chat_id    TEXT NOT NULL,
			user_id    TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			reason     TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS warnings (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			platform    TEXT NOT NULL,
			user_id     TEXT NOT NULL,
			reason      TEXT,
			warned_by   TEXT NOT NULL,
			timestamp   INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_warn_thresholds (
			platform       TEXT NOT NULL,
			user_id        TEXT NOT NULL,
			extra_warns    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (platform, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS mod_actions (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			platform       TEXT NOT NULL,
			moderator_id   TEXT NOT NULL,
			target_id      TEXT NOT NULL,
			action         TEXT NOT NULL,
			reason         TEXT,
			timestamp      INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS cases (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			case_number    INTEGER NOT NULL UNIQUE,
			action_type    TEXT NOT NULL,
			platform       TEXT NOT NULL,
			target_id      TEXT NOT NULL,
			moderator_id   TEXT NOT NULL,
			reason         TEXT,
			timestamp      INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS excluded_channels (
			guild_id       TEXT NOT NULL,
			channel_id     TEXT NOT NULL,
			PRIMARY KEY (guild_id, channel_id)
		)`,
		`CREATE TABLE IF NOT EXISTS mutes (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			platform       TEXT NOT NULL,
			chat_id        TEXT NOT NULL,
			user_id        TEXT NOT NULL,
			expires_at     INTEGER NOT NULL,
			reason         TEXT
		)`,
	}

	for _, m := range migrations {
		if _, err := d.conn.Exec(m); err != nil {
			return fmt.Errorf("executing migration: %w", err)
		}
	}

	return nil
}

// --- Notes ---

func (d *DB) GetNote(name string) (string, error) {
	var content string
	err := d.conn.QueryRow("SELECT content FROM notes WHERE name = ?", name).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return content, err
}

func (d *DB) ListNotes() ([]string, error) {
	rows, err := d.conn.Query("SELECT name FROM notes ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (d *DB) AddNote(name, content string) error {
	_, err := d.conn.Exec("INSERT INTO notes (name, content) VALUES (?, ?)", name, content)
	return err
}

func (d *DB) EditNote(name, content string) error {
	res, err := d.conn.Exec("UPDATE notes SET content = ? WHERE name = ?", content, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("note %q not found", name)
	}
	return nil
}

func (d *DB) DeleteNote(name string) error {
	res, err := d.conn.Exec("DELETE FROM notes WHERE name = ?", name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("note %q not found", name)
	}
	return nil
}

// --- Admins ---

func (d *DB) IsPermaAdmin(platform, userID string, cfg PermaAdminProvider) bool {
	ids := cfg.GetPermaAdminIDs(platform)
	for _, id := range ids {
		if id == userID {
			return true
		}
	}
	return false
}

func (d *DB) IsAdmin(platform, userID string, cfg PermaAdminProvider) bool {
	if d.IsPermaAdmin(platform, userID, cfg) {
		return true
	}
	var count int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM admins WHERE platform = ? AND user_id = ?", platform, userID).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

func (d *DB) AddAdmin(platform, userID, addedBy string) error {
	_, err := d.conn.Exec("INSERT OR IGNORE INTO admins (platform, user_id, added_by) VALUES (?, ?, ?)", platform, userID, addedBy)
	return err
}

func (d *DB) RemoveAdmin(platform, userID string) error {
	_, err := d.conn.Exec("DELETE FROM admins WHERE platform = ? AND user_id = ?", platform, userID)
	return err
}

// --- Timed Bans ---

type TimedBan struct {
	ID        int64
	Platform  string
	ChatID    string
	UserID    string
	ExpiresAt int64
	Reason    string
}

func (d *DB) AddTimedBan(platform, chatID, userID string, expiresAt int64, reason string) (int64, error) {
	res, err := d.conn.Exec(
		"INSERT INTO timed_bans (platform, chat_id, user_id, expires_at, reason) VALUES (?, ?, ?, ?, ?)",
		platform, chatID, userID, expiresAt, reason,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) DeleteTimedBan(id int64) error {
	_, err := d.conn.Exec("DELETE FROM timed_bans WHERE id = ?", id)
	return err
}

func (d *DB) GetPendingTimedBans() ([]TimedBan, error) {
	rows, err := d.conn.Query("SELECT id, platform, chat_id, user_id, expires_at, COALESCE(reason, '') FROM timed_bans")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bans []TimedBan
	for rows.Next() {
		var b TimedBan
		if err := rows.Scan(&b.ID, &b.Platform, &b.ChatID, &b.UserID, &b.ExpiresAt, &b.Reason); err != nil {
			return nil, err
		}
		bans = append(bans, b)
	}
	return bans, rows.Err()
}

// --- Warnings ---

type Warning struct {
	ID        int64
	Platform  string
	UserID    string
	Reason    string
	WarnedBy  string
	Timestamp int64
}

func (d *DB) AddWarning(platform, userID, reason, warnedBy string) (int64, error) {
	res, err := d.conn.Exec(
		"INSERT INTO warnings (platform, user_id, reason, warned_by, timestamp) VALUES (?, ?, ?, ?, ?)",
		platform, userID, reason, warnedBy, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) GetWarnings(platform, userID string) ([]Warning, error) {
	rows, err := d.conn.Query(
		"SELECT id, platform, user_id, COALESCE(reason, ''), warned_by, timestamp FROM warnings WHERE platform = ? AND user_id = ? ORDER BY id ASC",
		platform, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var warnings []Warning
	for rows.Next() {
		var w Warning
		if err := rows.Scan(&w.ID, &w.Platform, &w.UserID, &w.Reason, &w.WarnedBy, &w.Timestamp); err != nil {
			return nil, err
		}
		warnings = append(warnings, w)
	}
	return warnings, rows.Err()
}

func (d *DB) DeleteWarningByIndex(platform, userID string, index int) error {
	warnings, err := d.GetWarnings(platform, userID)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(warnings) {
		return fmt.Errorf("warning #%d out of range", index+1)
	}
	_, err = d.conn.Exec("DELETE FROM warnings WHERE id = ?", warnings[index].ID)
	return err
}

func (d *DB) GetWarningCount(platform, userID string) (int, error) {
	var count int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM warnings WHERE platform = ? AND user_id = ?", platform, userID).Scan(&count)
	return count, err
}

// --- Warn Thresholds ---

func (d *DB) GetWarnThreshold(platform, userID string) (int, error) {
	const defaultThreshold = 3
	var extra int
	err := d.conn.QueryRow(
		"SELECT extra_warns FROM user_warn_thresholds WHERE platform = ? AND user_id = ?",
		platform, userID,
	).Scan(&extra)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultThreshold, nil
	}
	if err != nil {
		return 0, err
	}
	return defaultThreshold + extra, nil
}

func (d *DB) IncrementWarnThreshold(platform, userID string) error {
	_, err := d.conn.Exec(
		`INSERT INTO user_warn_thresholds (platform, user_id, extra_warns)
		 VALUES (?, ?, 3)
		 ON CONFLICT(platform, user_id) DO UPDATE SET extra_warns = extra_warns + 3`,
		platform, userID,
	)
	return err
}

// --- Mod Actions ---

func (d *DB) LogModAction(platform, moderatorID, targetID, action, reason string) error {
	_, err := d.conn.Exec(
		"INSERT INTO mod_actions (platform, moderator_id, target_id, action, reason, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
		platform, moderatorID, targetID, action, reason, time.Now().Unix(),
	)
	return err
}

// --- Cases ---

type Case struct {
	ID          int64
	CaseNumber  int64
	ActionType  string
	Platform    string
	TargetID    string
	ModeratorID string
	Reason      string
	Timestamp   int64
}

func (d *DB) CreateCase(platform, actionType, targetID, moderatorID, reason string) (*Case, error) {
	now := time.Now().Unix()

	// Use a transaction to prevent race conditions in case number generation
	tx, err := d.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Get next case number within transaction
	var nextNum int64
	err = tx.QueryRow("SELECT COALESCE(MAX(case_number), 0) + 1 FROM cases").Scan(&nextNum)
	if err != nil {
		return nil, fmt.Errorf("getting next case number: %w", err)
	}

	res, err := tx.Exec(
		"INSERT INTO cases (case_number, action_type, platform, target_id, moderator_id, reason, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)",
		nextNum, actionType, platform, targetID, moderatorID, reason, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting case: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("getting insert ID: %w", err)
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &Case{
		ID:          id,
		CaseNumber:  nextNum,
		ActionType:  actionType,
		Platform:    platform,
		TargetID:    targetID,
		ModeratorID: moderatorID,
		Reason:      reason,
		Timestamp:   now,
	}, nil
}

func (d *DB) GetCase(caseNumber int64) (*Case, error) {
	var c Case
	err := d.conn.QueryRow(
		"SELECT id, case_number, action_type, platform, target_id, moderator_id, reason, timestamp FROM cases WHERE case_number = ?",
		caseNumber,
	).Scan(&c.ID, &c.CaseNumber, &c.ActionType, &c.Platform, &c.TargetID, &c.ModeratorID, &c.Reason, &c.Timestamp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (d *DB) GetCasesForUser(platform, userID string) ([]Case, error) {
	rows, err := d.conn.Query(
		"SELECT id, case_number, action_type, platform, target_id, moderator_id, reason, timestamp FROM cases WHERE platform = ? AND target_id = ? ORDER BY case_number DESC",
		platform, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cases []Case
	for rows.Next() {
		var c Case
		if err := rows.Scan(&c.ID, &c.CaseNumber, &c.ActionType, &c.Platform, &c.TargetID, &c.ModeratorID, &c.Reason, &c.Timestamp); err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, rows.Err()
}

// --- Mutes ---

type Mute struct {
	ID        int64
	Platform  string
	ChatID    string
	UserID    string
	ExpiresAt int64
	Reason    string
}

func (d *DB) AddMute(platform, chatID, userID string, expiresAt int64, reason string) (int64, error) {
	res, err := d.conn.Exec(
		"INSERT INTO mutes (platform, chat_id, user_id, expires_at, reason) VALUES (?, ?, ?, ?, ?)",
		platform, chatID, userID, expiresAt, reason,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) DeleteMute(id int64) error {
	_, err := d.conn.Exec("DELETE FROM mutes WHERE id = ?", id)
	return err
}

func (d *DB) GetPendingMutes() ([]Mute, error) {
	rows, err := d.conn.Query("SELECT id, platform, chat_id, user_id, expires_at, COALESCE(reason, '') FROM mutes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mutes []Mute
	for rows.Next() {
		var m Mute
		if err := rows.Scan(&m.ID, &m.Platform, &m.ChatID, &m.UserID, &m.ExpiresAt, &m.Reason); err != nil {
			return nil, err
		}
		mutes = append(mutes, m)
	}
	return mutes, rows.Err()
}

// --- Excluded Channels ---

func (d *DB) AddExcludedChannel(guildID, channelID string) error {
	_, err := d.conn.Exec("INSERT OR IGNORE INTO excluded_channels (guild_id, channel_id) VALUES (?, ?)", guildID, channelID)
	return err
}

func (d *DB) RemoveExcludedChannel(guildID, channelID string) error {
	_, err := d.conn.Exec("DELETE FROM excluded_channels WHERE guild_id = ? AND channel_id = ?", guildID, channelID)
	return err
}

func (d *DB) IsChannelExcluded(guildID, channelID string) (bool, error) {
	var count int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM excluded_channels WHERE guild_id = ? AND channel_id = ?", guildID, channelID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (d *DB) ListExcludedChannels(guildID string) ([]string, error) {
	rows, err := d.conn.Query("SELECT channel_id FROM excluded_channels WHERE guild_id = ?", guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// --- Interface for config dependency ---

type PermaAdminProvider interface {
	GetPermaAdminIDs(platform string) []string
}
