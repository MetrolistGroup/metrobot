package db

import (
	"path/filepath"
	"testing"
)

func TestGarminContextCutoffPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetGarminContextCutoff("channel", "123"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if got, err := database.GetGarminContextCutoff("channel"); err != nil || got != "123" {
		t.Fatalf("cutoff = %q, %v", got, err)
	}
}
