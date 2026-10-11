package db

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSearchCasesAllHistoryAndStablePagination(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "cases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 1; i <= 123; i++ {
		platform := "discord"
		if i%2 == 0 {
			platform = "telegram"
		}
		if _, err := d.conn.Exec(`INSERT INTO cases (case_number, action_type, platform, target_id, moderator_id, reason, timestamp) VALUES (?, 'warn', ?, 'target', 'moderator', NULL, ?)`, i, platform, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	filter := CaseSearch{}
	count, previous := 0, int64(124)
	for {
		items, cursor, err := d.SearchCases(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range items {
			if c.CaseNumber != previous-1 || c.Reason != "" {
				t.Fatalf("skipped or unordered case: %+v after %d", c, previous)
			}
			previous = c.CaseNumber
			count++
		}
		if cursor == "" {
			break
		}
		if len(items) != 50 {
			t.Fatalf("page size = %d", len(items))
		}
		filter.Before, _ = strconv.ParseInt(cursor, 10, 64)
	}
	if count != 123 {
		t.Fatalf("retrieved %d cases", count)
	}
	items, _, err := d.SearchCases(context.Background(), CaseSearch{Text: "123"})
	if err != nil || len(items) != 1 || items[0].CaseNumber != 123 {
		t.Fatalf("case number search: %+v %v", items, err)
	}
}

func TestSearchCasesUTCDateAndLiteralText(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "cases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	from := time.Date(2025, 3, 30, 0, 0, 0, 0, time.UTC)
	until := from.AddDate(0, 0, 1)
	for i, timestamp := range []int64{from.Unix() - 1, from.Unix(), until.Unix() - 1, until.Unix()} {
		if _, err := d.conn.Exec(`INSERT INTO cases (case_number, action_type, platform, target_id, moderator_id, reason, timestamp) VALUES (?, 'BAN', 'telegram', 'target-42', 'mod-63', 'Literal 100%_reason', ?)`, i+1, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"", "100%_", "ban", "target-42", "mod-63", "telegram"} {
		items, cursor, err := d.SearchCases(context.Background(), CaseSearch{From: from, Until: until, Text: text})
		if err != nil || len(items) != 2 || items[0].CaseNumber != 3 || items[1].CaseNumber != 2 || cursor != "" {
			t.Fatalf("query %q: %+v cursor=%q err=%v", text, items, cursor, err)
		}
	}
	for _, text := range []string{"' OR 1=1 --", "%missing", "999"} {
		items, _, err := d.SearchCases(context.Background(), CaseSearch{From: from, Until: until, Text: text})
		if err != nil || len(items) != 0 {
			t.Fatalf("query %q: %+v err=%v", text, items, err)
		}
	}
	items, _, err := d.SearchCases(context.Background(), CaseSearch{Text: "2"})
	if err != nil || len(items) != 4 {
		t.Fatalf("ID substring search: %+v %v", items, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := d.SearchCases(ctx, CaseSearch{}); err == nil {
		t.Fatal("ignored canceled context")
	}
}
