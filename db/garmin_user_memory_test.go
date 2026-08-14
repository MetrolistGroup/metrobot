package db

import (
	"path/filepath"
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
