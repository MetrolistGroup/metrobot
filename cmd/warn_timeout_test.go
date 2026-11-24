package cmd

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

func newWarnMuteHandlers(t *testing.T) (*db.DB, *recordingScheduler, *ModerationHandler, *WarnHandler) {
	t.Helper()
	d := openTimerTestDB(t)
	s := &recordingScheduler{}
	m := &ModerationHandler{DB: d, Scheduler: s}
	return d, s, m, &WarnHandler{DB: d, Moderation: m}
}

func TestWarnTimeoutReplacesShorterMute(t *testing.T) {
	d, s, m, w := newWarnMuteHandlers(t)
	b := newStateBanner()
	if _, _, err := m.Mute(b, "mod", "400", 30*time.Minute, "short"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := w.Warn(b, "mod", "400", "reason"); err != nil {
		t.Fatal(err)
	}
	if len(s.ids) != 2 || s.ids[1] != "mute_2" {
		t.Fatalf("warning timeout was not scheduled as a stored mute: %v", s.ids)
	}
	rows, err := d.GetPendingMutes()
	if err != nil || len(rows) != 1 || rows[0].ID != 2 || rows[0].ExpiresAt != b.mutedUntil {
		t.Fatalf("restart should see only the warning timeout: rows=%+v mutedUntil=%d err=%v", rows, b.mutedUntil, err)
	}
	if b.mutedUntil < time.Now().Add(59*time.Minute).Unix() {
		t.Fatalf("warning timeout ends too early: %d", b.mutedUntil)
	}

	s.callbacks[0]() // the 30-minute mute's timer
	if b.mutedUntil == 0 || b.unmuteCalls != 0 {
		t.Fatal("old mute timer ended the newer warning timeout")
	}
	s.callbacks[1]()
	rows, _ = d.GetPendingMutes()
	if b.mutedUntil != 0 || len(rows) != 0 {
		t.Fatalf("warning timeout should expire: mutedUntil=%d rows=%d", b.mutedUntil, len(rows))
	}
}

func TestMuteReplacesWarningTimeout(t *testing.T) {
	d, s, m, w := newWarnMuteHandlers(t)
	b := newStateBanner()
	if _, _, _, err := w.Warn(b, "mod", "400", "reason"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Mute(b, "mod", "400", 10*time.Minute, "manual"); err != nil {
		t.Fatal(err)
	}
	s.callbacks[0]() // the warning timeout's timer
	if b.mutedUntil == 0 || b.unmuteCalls != 0 {
		t.Fatal("old warning timer ended the newer manual mute")
	}
	s.callbacks[1]()
	rows, _ := d.GetPendingMutes()
	if b.mutedUntil != 0 || len(rows) != 0 {
		t.Fatalf("manual mute should expire: mutedUntil=%d rows=%d", b.mutedUntil, len(rows))
	}
}

func TestFailedWarnTimeoutKeepsExistingMute(t *testing.T) {
	d, s, m, w := newWarnMuteHandlers(t)
	b := newStateBanner()
	if _, _, err := m.Mute(b, "mod", "400", 30*time.Minute, "short"); err != nil {
		t.Fatal(err)
	}
	mutedUntil := b.mutedUntil
	b.restrictErr = errors.New("api down")
	text, _, _, err := w.Warn(b, "mod", "400", "reason")
	if err != nil || !strings.Contains(text, "timeout failed") {
		t.Fatalf("text=%q err=%v", text, err)
	}
	rows, _ := d.GetPendingMutes()
	if len(rows) != 1 || rows[0].ID != 1 || b.mutedUntil != mutedUntil || len(s.callbacks) != 1 {
		t.Fatalf("failed timeout changed the stored mute: rows=%+v timers=%d", rows, len(s.callbacks))
	}
	b.restrictErr = nil
	s.callbacks[0]()
	if b.mutedUntil != 0 {
		t.Fatal("existing mute should still expire")
	}
}

// blockInserts makes later inserts into table fail through a second connection.
func blockInserts(t *testing.T, path, table string) {
	t.Helper()
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TRIGGER block_` + table + ` BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
}

func TestUnwarnReportsAuditFailureAfterDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unwarn.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.AddWarning("discord", "400", "first", "mod"); err != nil {
		t.Fatal(err)
	}
	blockInserts(t, path, "mod_actions")
	blockInserts(t, path, "cases")
	h := &WarnHandler{DB: d, CaseHandler: &CaseHandler{DB: d}}
	text, c, err := h.Unwarn("discord", "mod", "400", 1, newStateBanner())
	if err != nil || c != nil {
		t.Fatalf("deletion succeeded, so Unwarn should not fail: case=%v err=%v", c, err)
	}
	if !strings.Contains(text, "Warning #1 removed") || !strings.Contains(text, "log entry could not be saved") || !strings.Contains(text, "case record could not be saved") {
		t.Fatalf("response should disclose audit failures: %q", text)
	}
	if count, _ := d.GetWarningCount("discord", "400"); count != 0 {
		t.Fatalf("warning should be deleted, count=%d", count)
	}
}
