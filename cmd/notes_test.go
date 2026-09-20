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

func TestDonateNoteRandomizesDeveloperOrder(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler := &NotesHandler{DB: database}
	const intro = "Enjoying the app? Consider supporting the people behind it:"
	developers := []string{
		"Mo: https://buymeacoffee.com/mostafaalagamy",
		"Nyx: https://github.com/sponsors/nyxiereal",
		"Adriel: https://github.com/sponsors/adrielggmotion",
		"Lamp: https://github.com/sponsors/l6t9",
	}
	if err := handler.AddNote("donate", "Support the developers", intro+"\n"+strings.Join(developers, "\n")); err != nil {
		t.Fatal(err)
	}

	seenFirst := map[string]bool{}
	for range 50 {
		content, err := handler.GetNote("donate")
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(content, "\n")
		if len(lines) != len(developers)+1 || lines[0] != intro {
			t.Fatalf("donate note structure = %q", content)
		}
		seen := map[string]bool{}
		for _, line := range lines[1:] {
			seen[line] = true
		}
		for _, developer := range developers {
			if !seen[developer] {
				t.Fatalf("donate note missing %q: %q", developer, content)
			}
		}
		seenFirst[lines[1]] = true
	}
	if len(seenFirst) < 2 {
		t.Fatal("donate note order did not change")
	}
}

func TestRenameNotePreservesContentAndDescription(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler := &NotesHandler{DB: database}

	if err := handler.AddNote("update", "How to update", "Download the APK."); err != nil {
		t.Fatal(err)
	}
	if err := handler.RenameNote("UPDATE", " up "); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.GetNote("update"); err == nil {
		t.Fatal("old note name still exists")
	}
	if content, err := handler.GetNote("up"); err != nil || content != "Download the APK." {
		t.Fatalf("renamed note content = %q, %v", content, err)
	}
	if list, err := handler.ListNotes(); err != nil || !strings.Contains(list, "`up` - How to update") {
		t.Fatalf("renamed note list = %q, %v", list, err)
	}
}
