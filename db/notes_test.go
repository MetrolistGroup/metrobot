package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationAddsNoteDescriptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE notes (name TEXT PRIMARY KEY, content TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO notes VALUES ('playback', 'Restart the app.')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	notes, err := database.ListNotes()
	if err != nil || len(notes) != 1 || notes[0].Name != "playback" || notes[0].ShortDesc != "" {
		t.Fatalf("migrated notes = %#v, %v", notes, err)
	}
}
