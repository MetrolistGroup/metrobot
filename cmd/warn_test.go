package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/db"
)

func TestWarningsAreOneIndexed(t *testing.T) {
	database := openWarnTestDB(t)
	handler := &WarnHandler{DB: database}

	if _, err := database.AddWarning("discord", "target", "first", "mod-1"); err != nil {
		t.Fatalf("AddWarning first: %v", err)
	}
	if _, err := database.AddWarning("discord", "target", "second", "mod-2"); err != nil {
		t.Fatalf("AddWarning second: %v", err)
	}

	got, err := handler.Warnings("discord", "target")
	if err != nil {
		t.Fatalf("Warnings: %v", err)
	}

	if !strings.Contains(got, "[1] first") || !strings.Contains(got, "[2] second") {
		t.Fatalf("Warnings() output not 1-indexed: %q", got)
	}
}

func TestUnwarnUsesOneBasedIDs(t *testing.T) {
	database := openWarnTestDB(t)
	handler := &WarnHandler{DB: database}

	if _, err := database.AddWarning("discord", "target", "first", "mod-1"); err != nil {
		t.Fatalf("AddWarning first: %v", err)
	}
	if _, err := database.AddWarning("discord", "target", "second", "mod-2"); err != nil {
		t.Fatalf("AddWarning second: %v", err)
	}

	resp, err := handler.Unwarn("discord", "caller", "target", 1)
	if err != nil {
		t.Fatalf("Unwarn: %v", err)
	}
	if !strings.Contains(resp, "Warning #1 removed") {
		t.Fatalf("Unwarn() response = %q", resp)
	}

	got, err := handler.Warnings("discord", "target")
	if err != nil {
		t.Fatalf("Warnings after unwarn: %v", err)
	}
	if strings.Contains(got, "[1] first") {
		t.Fatalf("first warning should have been removed: %q", got)
	}
	if !strings.Contains(got, "[1] second") {
		t.Fatalf("remaining warning should be renumbered to 1: %q", got)
	}

	if _, err := handler.Unwarn("discord", "caller", "target", 0); err == nil {
		t.Fatal("Unwarn() with id 0 should fail")
	}
}

func openWarnTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "warn-test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database
}
