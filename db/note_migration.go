package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

// migrateNoteMarkup records completion even on an empty database: names are never magic again.
func (d *DB) migrateNoteMarkup() error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS migrations (name TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	var hasMarkupEnabled int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('notes') WHERE name = 'markup_enabled'`).Scan(&hasMarkupEnabled); err != nil {
		return err
	}
	if hasMarkupEnabled == 0 {
		if _, err := tx.Exec(`ALTER TABLE notes ADD COLUMN markup_enabled INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}
	var done int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM migrations WHERE name = 'note_markup_v1'`).Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return tx.Commit()
	}
	// Ordinary legacy notes may contain directive examples, even valid JSON; leave their bodies literal.
	if _, err := tx.Exec(`UPDATE notes SET markup_enabled = 0 WHERE name NOT IN ('donate', 'kmp')`); err != nil {
		return err
	}
	for _, name := range []string{"donate", "kmp"} {
		var content string
		err := tx.QueryRow(`SELECT content FROM notes WHERE name = ?`, name).Scan(&content)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		// Leave invalid legacy bytes intact; JSON marshaling would silently replace them.
		if !utf8.ValidString(content) || notefmt.HasDirectives(notefmt.Normalize(content)) || strings.TrimSpace(content) == "" {
			continue
		}
		migrated := content
		if name == "kmp" {
			migrated = notefmt.Fence("faq", content)
		} else {
			// Match the legacy retrieval's newline normalization before defining its entries.
			intro, rest, found := strings.Cut(notefmt.Normalize(content), "\n")
			if found && strings.TrimSpace(rest) != "" {
				// Blank legacy lines travel with a neighboring entry, rather than creating invalid empty entries.
				var entries []string
				pending := ""
				for _, line := range strings.Split(rest, "\n") {
					if strings.TrimSpace(line) == "" {
						if len(entries) == 0 {
							pending += line + "\n"
						} else {
							entries[len(entries)-1] += "\n" + line
						}
					} else {
						entries = append(entries, pending+line)
						pending = ""
					}
				}
				body, err := json.Marshal(entries)
				if err != nil {
					return err
				}
				migrated = intro + "\n" + notefmt.Fence("random", string(body))
			}
		}
		if _, err := tx.Exec(`UPDATE notes SET content = ? WHERE name = ?`, migrated, name); err != nil {
			return fmt.Errorf("migrating %s: %w", name, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO migrations (name) VALUES ('note_markup_v1')`); err != nil {
		return err
	}
	return tx.Commit()
}
