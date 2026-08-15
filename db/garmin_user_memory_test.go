package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGarminUserMemoryRoundTripAndDelete(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if memory, err := database.GetGarminUserMemory("discord", "user"); err != nil || !memory.Empty() {
		t.Fatalf("missing memory = %#v, %v", memory, err)
	}
	want := GarminUserMemory{Info: "likes cats", Pronouns: "they/them", Bio: "cat enjoyer"}
	if err := database.SetGarminUserMemory("discord", "user", want); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetGarminUserMemory("discord", "user")
	if err != nil || got.Info != want.Info || got.Pronouns != want.Pronouns || got.Bio != want.Bio || got.UpdatedAt.IsZero() {
		t.Fatalf("stored memory = %#v, %v", got, err)
	}
	if err := database.DeleteGarminUserMemory("discord", "user"); err != nil {
		t.Fatal(err)
	}
	if got, err := database.GetGarminUserMemory("discord", "user"); err != nil || !got.Empty() {
		t.Fatalf("deleted memory = %#v, %v", got, err)
	}
}

func TestGarminUserMemoryRejectsOversizedFields(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SetGarminUserMemory("discord", "user", GarminUserMemory{Info: strings.Repeat("x", garminUserMemoryInfoLimit+1)}); err == nil {
		t.Fatal("oversized Garmin user memory was accepted")
	}
}

func TestGarminMemoryConsentAndListing(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	consent, err := database.GetGarminMemoryConsent("discord", "user")
	if err != nil || consent.Decided || consent.Enabled {
		t.Fatalf("initial consent = %#v, %v", consent, err)
	}
	if err := database.SetGarminMemoryConsent("discord", "user", true); err != nil {
		t.Fatal(err)
	}
	consent, err = database.GetGarminMemoryConsent("discord", "user")
	if err != nil || !consent.Decided || !consent.Enabled {
		t.Fatalf("enabled consent = %#v, %v", consent, err)
	}
	if err := database.SetGarminUserMemory("discord", "user", GarminUserMemory{Info: "likes cats"}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetGarminUserMemory("discord", "other", GarminUserMemory{Bio: "maintainer"}); err != nil {
		t.Fatal(err)
	}
	entries, err := database.ListGarminUserMemories("discord")
	if err != nil || len(entries) != 2 {
		t.Fatalf("listed memories = %#v, %v", entries, err)
	}

	if err := database.SetGarminMemoryConsent("discord", "user", false); err != nil {
		t.Fatal(err)
	}
	consent, err = database.GetGarminMemoryConsent("discord", "user")
	if err != nil || !consent.Decided || consent.Enabled {
		t.Fatalf("disabled consent = %#v, %v", consent, err)
	}
	if memory, err := database.GetGarminUserMemory("discord", "user"); err != nil || !memory.Empty() {
		t.Fatalf("memory after opt-out = %#v, %v", memory, err)
	}
	entries, err = database.ListGarminUserMemories("discord")
	if err != nil || len(entries) != 1 || entries[0].UserID != "other" {
		t.Fatalf("memories after opt-out = %#v, %v", entries, err)
	}
}
