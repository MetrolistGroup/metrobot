package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestKudosDailySpendAndLeaderboard(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Unix(1_800_000_000, 0)
	// Streak 1 gets no multiplier bonus: 5000 + randomPart + streak.
	if amount, streak, balance, _, ok, err := database.ClaimDaily("a", 123, now); err != nil || !ok || streak != 1 || amount != 5124 || balance != 5124 {
		t.Fatalf("first claim = %d %d %d %v %v", amount, streak, balance, ok, err)
	}
	if _, streak, balance, next, ok, err := database.ClaimDaily("a", 123, now.Add(time.Hour)); err != nil || ok || streak != 1 || balance != 5124 || !next.Equal(now.Add(DailyCooldown)) {
		t.Fatalf("cooldown claim = %d %d %v %v %v", streak, balance, next, ok, err)
	}
	// Streak 2 adds the 1600*streak bonus: 5000 + 0 + 3200 + 2 = 8202.
	if amount, streak, balance, _, ok, err := database.ClaimDaily("a", 0, now.Add(DailyCooldown+time.Second)); err != nil || !ok || streak != 2 || amount != 8202 || balance != 13326 {
		t.Fatalf("next day claim = %d %d %d %v %v", amount, streak, balance, ok, err)
	}
	if _, ok, err := database.SpendKudos("a", 13327); err != nil || ok {
		t.Fatalf("overspend allowed: %v %v", ok, err)
	}
	if _, ok, err := database.SpendKudos("nobody", 1); err != nil || ok {
		t.Fatalf("spend without account: %v %v", ok, err)
	}
	if balance, ok, err := database.SpendKudos("a", 13026); err != nil || !ok || balance != 300 {
		t.Fatalf("spend = %d %v %v", balance, ok, err)
	}
	if balance, err := database.AddKudos("b", 900); err != nil || balance != 900 {
		t.Fatalf("add = %d %v", balance, err)
	}
	entries, err := database.KudosLeaderboard(10)
	if err != nil || len(entries) != 2 || entries[0].UserID != "b" || entries[1].Balance != 300 {
		t.Fatalf("leaderboard = %#v %v", entries, err)
	}
	if rank, err := database.KudosRank("a"); err != nil || rank != 2 {
		t.Fatalf("rank = %d %v", rank, err)
	}
}

func TestStarLeaderboard(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.AddBoardEntry("starboard", "m1", "c", "g", "a", "", 5, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddBoardEntry("sobboard", "m2", "c", "g", "a", "", 3, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddBoardEntry("starboard", "m3", "c", "g", "b", "", 10, 3); err != nil {
		t.Fatal(err)
	}
	entries, err := database.StarLeaderboard(10)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %#v %v", entries, err)
	}
	if entries[0].UserID != "b" || entries[0].Stars != 10 || entries[0].Posts != 1 {
		t.Fatalf("top = %#v", entries[0])
	}
	if entries[1].UserID != "a" || entries[1].Stars != 8 || entries[1].Posts != 2 {
		t.Fatalf("second = %#v", entries[1])
	}
}
