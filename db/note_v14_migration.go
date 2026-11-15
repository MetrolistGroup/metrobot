package db

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

//go:embed note_versions/*.json
var managedNoteVersions embed.FS

type managedNote struct {
	Name        string `json:"name"`
	Description string `json:"short_desc"`
	Content     string `json:"content"`
}

func readManagedNotes(path string) ([]managedNote, error) {
	data, err := managedNoteVersions.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var notes []managedNote
	err = json.Unmarshal(data, &notes)
	return notes, err
}

// migrateV14Notes never overwrites an unknown body or a staff-edited description.
func (d *DB) migrateV14Notes() error {
	current, err := readManagedNotes("note_versions/v14.json")
	if err != nil {
		return err
	}
	paths, err := managedNoteVersions.ReadDir("note_versions")
	if err != nil {
		return err
	}
	previous := make(map[string][]managedNote)
	for _, path := range paths {
		if !strings.HasPrefix(path.Name(), "pre_v14") {
			continue
		}
		notes, err := readManagedNotes("note_versions/" + path.Name())
		if err != nil {
			return err
		}
		for _, note := range notes {
			previous[note.Name] = append(previous[note.Name], note)
		}
	}
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM migrations WHERE name = 'metrolist_v14_notes_v1'`).Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return tx.Commit()
	}
	var outcomes []string
	for _, note := range current {
		var content, description string
		err := tx.QueryRow(`SELECT content, short_desc FROM notes WHERE name = ?`, note.Name).Scan(&content, &description)
		if err == sql.ErrNoRows {
			outcomes = append(outcomes, fmt.Sprintf("%s: skipped, absent", note.Name))
			continue
		}
		if err != nil {
			return err
		}
		if content == note.Content && description == note.Description {
			outcomes = append(outcomes, fmt.Sprintf("%s: skipped, already current", note.Name))
			continue
		}
		known := false
		for _, old := range previous[note.Name] {
			// note_markup_v1 may have wrapped the known kmp body on a legacy database.
			bodyMatches := content == old.Content || (note.Name == "kmp" && content == notefmt.Fence("faq", old.Content))
			if bodyMatches && description == old.Description {
				known = true
				break
			}
		}
		if !known {
			outcomes = append(outcomes, fmt.Sprintf("%s: skipped, staff-edited or unknown version; manual review required", note.Name))
			continue
		}
		if _, err := tx.Exec(`UPDATE notes SET content = ?, short_desc = ?, markup_enabled = 1 WHERE name = ?`, note.Content, note.Description, note.Name); err != nil {
			return err
		}
		outcomes = append(outcomes, fmt.Sprintf("%s: updated", note.Name))
	}
	if _, err := tx.Exec(`INSERT INTO migrations (name) VALUES ('metrolist_v14_notes_v1')`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, outcome := range outcomes {
		log.Printf("Metrolist v14 note migration: %s", outcome)
	}
	return nil
}
