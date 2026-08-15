package db

import (
	"path/filepath"
	"testing"
)

func TestMigrationDeletesLegacyGarminUserMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE garmin_user_memory (platform TEXT, user_id TEXT, info TEXT)`,
		`CREATE TABLE garmin_memory_consent (platform TEXT, user_id TEXT, memory_enabled INTEGER)`,
		`INSERT INTO garmin_user_memory VALUES ('discord', 'user', 'private preference')`,
		`INSERT INTO garmin_memory_consent VALUES ('discord', 'user', 1)`,
	} {
		if _, err := database.conn.Exec(statement); err != nil {
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
	defer database.Close()
	for _, table := range []string{"garmin_user_memory", "garmin_memory_consent"} {
		var count int
		if err := database.conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("legacy table %q still exists", table)
		}
	}
}
