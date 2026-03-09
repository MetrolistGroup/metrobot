package db

import (
	"database/sql"
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
	if err == sql.ErrNoRows {
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
	return 3, nil
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

// --- Interface for config dependency ---

type PermaAdminProvider interface {
	GetPermaAdminIDs(platform string) []string
}
