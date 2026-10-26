package cmd

import (
	"encoding/json"
	"os"
	"testing"
)

func TestAuthoredSupportNotesAreValid(t *testing.T) {
	data, err := os.ReadFile("../docs/support-notes.json")
	if err != nil {
		t.Fatal(err)
	}
	var notes []struct {
		Name        string `json:"name"`
		Description string `json:"short_desc"`
		Content     string `json:"content"`
	}
	if err := json.Unmarshal(data, &notes); err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 {
		t.Fatal("empty support note update")
	}
	seen := make(map[string]bool)
	for _, note := range notes {
		if seen[note.Name] {
			t.Fatalf("duplicate note %q", note.Name)
		}
		seen[note.Name] = true
		name, description, content, err := ValidateNote(note.Name, note.Description, note.Content)
		if err != nil {
			t.Fatalf("note %s: %v", note.Name, err)
		}
		if name != note.Name || description != note.Description || content != note.Content {
			t.Fatalf("note %s needs normalization before deployment", note.Name)
		}
	}
}
