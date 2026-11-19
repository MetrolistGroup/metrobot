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
	if balance, _, ok, err := database.ClaimDaily("a", 250, 24*time.Hour, now); err != nil || !ok || balance != 250 {
		t.Fatalf("first claim = %d %v %v", balance, ok, err)
	}
	if balance, next, ok, err := database.ClaimDaily("a", 250, 24*time.Hour, now.Add(time.Hour)); err != nil || ok || balance != 250 || !next.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("cooldown claim = %d %v %v %v", balance, next, ok, err)
	}
	if balance, _, ok, err := database.ClaimDaily("a", 250, 24*time.Hour, now.Add(24*time.Hour)); err != nil || !ok || balance != 500 {
		t.Fatalf("next day claim = %d %v %v", balance, ok, err)
	}
	if _, ok, err := database.SpendKudos("a", 501); err != nil || ok {
		t.Fatalf("overspend allowed: %v %v", ok, err)
	}
	if _, ok, err := database.SpendKudos("nobody", 1); err != nil || ok {
		t.Fatalf("spend without account: %v %v", ok, err)
	}
	if balance, ok, err := database.SpendKudos("a", 200); err != nil || !ok || balance != 300 {
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
