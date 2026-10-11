package db

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestGarminSystemPromptPersistsAndResets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { database.Close() }()
	if content, overridden, err := database.GetGarminSystemPrompt(); err != nil || overridden || content != "" {
		t.Fatalf("default = %q, %v, %v", content, overridden, err)
	}
	const content = "  custom base\nwith literal \\n and é\r\n  "
	for _, value := range []string{"initial", content} {
		if err := database.SetGarminSystemPrompt(value); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, overridden, err := database.GetGarminSystemPrompt(); err != nil || !overridden || got != content {
		t.Fatalf("reopened = %q, %v, %v", got, overridden, err)
	}
	for range 2 {
		if err := database.ResetGarminSystemPrompt(); err != nil {
			t.Fatal(err)
		}
	}
	if got, overridden, err := database.GetGarminSystemPrompt(); err != nil || overridden || got != "" {
		t.Fatalf("reset = %q, %v, %v", got, overridden, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, overridden, err := database.GetGarminSystemPrompt(); err != nil || overridden {
		t.Fatalf("reset did not persist: %v, %v", overridden, err)
	}
}

func TestGarminSystemPromptValidationAndFailedWritesPreserveOverride(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	content := strings.Repeat("é", GarminPromptMaxSize/2)
	if err := database.SetGarminSystemPrompt(content); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", " \r\n\t", "\u2003", string([]byte{0xff}), content + "x"} {
		if err := database.SetGarminSystemPrompt(invalid); !errors.Is(err, ErrInvalidGarminPrompt) {
			t.Fatalf("invalid prompt returned %v", err)
		}
		if got, overridden, err := database.GetGarminSystemPrompt(); err != nil || !overridden || got != content {
			t.Fatalf("validation changed override: overridden %v, error %v", overridden, err)
		}
	}
	for _, statement := range []string{
		`CREATE TRIGGER reject_prompt_update BEFORE UPDATE ON settings BEGIN SELECT RAISE(ABORT, 'write rejected'); END`,
		`CREATE TRIGGER reject_prompt_delete BEFORE DELETE ON settings BEGIN SELECT RAISE(ABORT, 'delete rejected'); END`,
	} {
		if _, err := database.conn.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetGarminSystemPrompt("new"); err == nil {
		t.Fatal("failed upsert reported success")
	}
	if err := database.ResetGarminSystemPrompt(); err == nil {
		t.Fatal("failed reset reported success")
	}
	if got, overridden, err := database.GetGarminSystemPrompt(); err != nil || !overridden || got != content {
		t.Fatalf("failed write changed override: overridden %v, error %v", overridden, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.GetGarminSystemPrompt(); err == nil {
		t.Fatal("read error was hidden as default")
	}
}
