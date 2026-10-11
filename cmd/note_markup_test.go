package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

func TestNoteNamesAreNotMagicAndInvalidEditPreservesRawBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := &NotesHandler{DB: database}
	plain := "intro\nfirst\nsecond\nthird"
	for _, name := range []string{"donate", "kmp"} {
		if err := h.AddNote(name, "Plain", plain); err != nil {
			t.Fatal(err)
		}
		for range 10 {
			got, err := h.GetNote(name)
			if err != nil || got != plain {
				t.Fatalf("name still magic: %q, %v", got, err)
			}
		}
	}
	raw := "Before\n" + notefmt.Fence("components-v2", `{"components":[{"type":"text","content":"line\nnext and literal \\n"}]}`) + "\nAfter"
	if err := h.AddNote("layout", "Layout", raw); err != nil {
		t.Fatal(err)
	}
	if got, err := h.GetNote("layout"); err != nil || got != "Before\nline\nnext and literal \\n\nAfter" {
		t.Fatalf("fallback = %q, %v", got, err)
	}
	for _, invalid := range []string{
		notefmt.Fence("components-v2", `{"components":[{"type":"text","content":"hi","custom_id":"evil"}]}`),
		notefmt.Fence("components-v2", `{"components":[{"type":"text","content":"`+strings.Repeat("𝄞", 2001)+`"}]}`),
		notefmt.Fence("faq", "# No questions"),
		notefmt.Fence("random", `["\uD800"]`),
		raw + "\n" + notefmt.Fence("faq", "## Q\nA"),
	} {
		if err := h.EditNote("layout", "Invalid", invalid); err == nil {
			t.Fatal("invalid markup saved")
		}
		got, err := database.GetNote("layout")
		if err != nil || got != raw {
			t.Fatal("invalid edit replaced existing raw content")
		}
	}
	database.Close()
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	h.DB = database
	for _, name := range []string{"donate", "kmp"} {
		if got, err := h.GetNote(name); err != nil || got != plain {
			t.Fatal("restart applied magic-name migration to a new note")
		}
	}
}
