package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/db"
)

func TestNormalizeNoteContent(t *testing.T) {
	input := "# Playback issues\\nUse high quality\r\nClear cache\\r\\nDone"
	want := "# Playback issues\nUse high quality\nClear cache\nDone"

	if got := normalizeNoteContent(input); got != want {
		t.Fatalf("normalizeNoteContent() = %q, want %q", got, want)
	}
}

func TestNotesRequireSearchableNameAndDescription(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler := &NotesHandler{DB: database}

	if err := handler.AddNote("play back", "Playback help", "Restart the app."); err == nil || !strings.Contains(err.Error(), "spaces") {
		t.Fatalf("spaced note name error = %v", err)
	}
	if err := handler.AddNote("playback", "Playback stops unexpectedly", "Restart the app.\\nTry again."); err != nil {
		t.Fatal(err)
	}
	list, err := handler.ListNotes()
	if err != nil || !strings.Contains(list, "Playback stops unexpectedly") {
		t.Fatalf("note list = %q, %v", list, err)
	}
	if content, err := handler.GetNote("playback"); err != nil || content != "Restart the app.\nTry again." {
		t.Fatalf("note content = %q, %v", content, err)
	}
}
