package cmd

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

func TestLegacyDirectiveExamplesRemainLiteralUntilEdited(t *testing.T) {
	bodies := map[string]string{
		"invalid-random": "Before\n```random\nnot JSON\n```\nAfter",
		"valid-random":   "Before\n```random\n[\"one\\nparagraph\", \"two\"]\n```\nAfter",
		"faq":            "Before\n```faq\n# Example\n## Question\nAnswer\n```\nAfter",
		"invalid-layout": "Before\n```components-v2\nnot JSON\n```\nAfter",
		"valid-layout":   "Before\n```components-v2\n{\"components\":[{\"type\":\"text\",\"content\":\"Example\"}]}\n```\nAfter",
		"unclosed":       "Before\n```random\nnot JSON",
		"kmp":            "Before\n```random\nnot JSON\n```\nAfter",
		"donate":         "Before\n```random\n[\"one\\nparagraph\", \"two\"]\n```\nAfter",
	}
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE notes (name TEXT PRIMARY KEY, content TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for name, body := range bodies {
		if _, err := legacy.Exec(`INSERT INTO notes VALUES (?, ?)`, name, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	h := &NotesHandler{DB: database}
	assertLiteral := func(name, want string) {
		t.Helper()
		// GetNote serves Telegram/AI; GetNoteDocument serves every Discord delivery path.
		for range 3 {
			if got, err := h.GetNote(name); err != nil || got != want {
				t.Fatalf("%s readable note = %q, %v", name, got, err)
			}
			note, err := h.GetNoteDocument(name)
			if err != nil {
				t.Fatal(err)
			}
			r := note.Render()
			if r.Text != want || r.FAQ != nil || r.Layout != nil {
				t.Fatalf("%s example interpreted as markup: %#v", name, r)
			}
		}
		if got, err := h.DB.GetNote(name); err != nil || got != want {
			t.Fatalf("raw body changed: %q, %v", got, err)
		}
	}
	for name, body := range bodies {
		assertLiteral(name, body)
		if err := h.RenameNote(name, "saved-"+name); err != nil {
			t.Fatal(err)
		}
		assertLiteral("saved-"+name, body)
	}
	notes, err := database.ListNotes()
	if err != nil || len(notes) != len(bodies) {
		t.Fatalf("notes = %#v, %v", notes, err)
	}
	for _, note := range notes {
		got, err := database.GetNote(note.Name)
		if err != nil || got != bodies[strings.TrimPrefix(note.Name, "saved-")] {
			t.Fatalf("stored body changed: %q, %v", got, err)
		}
	}
	for _, name := range []string{"invalid-random", "kmp"} {
		if err := h.EditNote("saved-"+name, "Invalid", bodies[name]); err == nil {
			t.Fatal("invalid edit passed validation")
		}
		assertLiteral("saved-"+name, bodies[name])
	}
	active := "Before\n" + notefmt.Fence("random", `["one"]`) + "\nAfter"
	for _, name := range []string{"saved-valid-random", "saved-donate"} {
		if err := h.EditNote(name, "Authored", active); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.AddNote("new-markup", "Authored", active); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h.DB = database
	for name, body := range bodies {
		if name != "valid-random" && name != "donate" {
			assertLiteral("saved-"+name, body)
		}
	}
	for _, name := range []string{"saved-valid-random", "saved-donate", "new-markup"} {
		if got, err := h.GetNote(name); err != nil || got != "Before\none\nAfter" {
			t.Fatalf("%s authored note = %q, %v", name, got, err)
		}
	}
}
