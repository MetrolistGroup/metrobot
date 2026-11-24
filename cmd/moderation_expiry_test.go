package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type recordingScheduler struct {
	ids       []string
	callbacks []func()
}

func (s *recordingScheduler) AddTimer(id string, _ time.Duration, f func()) {
	s.ids = append(s.ids, id)
	s.callbacks = append(s.callbacks, f)
}

// stateBanner tracks whether the target is banned or muted.
type stateBanner struct {
	fakeModerationBanner
	banned     bool
	banCalls   int
	mutedUntil int64
	unbanErr   error
	unmuteErr  error
}

func (b *stateBanner) Ban(_, _ string) error {
	b.banCalls++
	b.banned = true
	return nil
}
func (b *stateBanner) Unban(_ string) error {
	b.unbanCalls++
	if b.unbanErr != nil {
		return b.unbanErr
	}
	b.banned = false
	return nil
}
func (b *stateBanner) Restrict(_ string, until int64) error {
	if b.restrictErr != nil {
		return b.restrictErr
	}
	b.mutedUntil = until
	return nil
}
func (b *stateBanner) Unrestrict(_ string) error {
	b.unmuteCalls++
	if b.unmuteErr != nil {
		return b.unmuteErr
	}
	b.mutedUntil = 0
	return nil
}

func newStateBanner() *stateBanner {
	return &stateBanner{fakeModerationBanner: fakeModerationBanner{platform: "discord", chatID: "300"}}
}

func TestStaleMuteTimerKeepsNewerMute(t *testing.T) {
	d := openTimerTestDB(t)
	s := &recordingScheduler{}
	h := &ModerationHandler{DB: d, Scheduler: s}
	b := newStateBanner()
	if _, _, err := h.Mute(b, "mod", "400", time.Hour, "one"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.Mute(b, "mod", "400", 24*time.Hour, "two"); err != nil {
		t.Fatal(err)
	}
	s.callbacks[0]()
	if b.mutedUntil == 0 || b.unmuteCalls != 0 {
		t.Fatalf("stale timer lifted the newer mute: mutedUntil=%d unmuteCalls=%d", b.mutedUntil, b.unmuteCalls)
	}
	s.callbacks[1]()
	rows, err := d.GetPendingMutes()
	if err != nil || len(rows) != 0 || b.mutedUntil != 0 {
		t.Fatalf("current timer should unmute and clear rows: rows=%d mutedUntil=%d err=%v", len(rows), b.mutedUntil, err)
	}
}

func TestStaleTimedBanTimerKeepsPermanentBan(t *testing.T) {
	for _, permanent := range []string{"ban", "dban"} {
		d := openTimerTestDB(t)
		s := &recordingScheduler{}
		h := &ModerationHandler{DB: d, Scheduler: s}
		b := newStateBanner()
		if _, _, err := h.TBan(b, "mod", "400", time.Hour, "one"); err != nil {
			t.Fatal(err)
		}
		var err error
		if permanent == "ban" {
			_, _, err = h.Ban(b, "mod", "400", "permanent")
		} else {
			_, _, err = h.DBan(b, "mod", "400", "permanent")
		}
		if err != nil {
			t.Fatal(err)
		}
		s.callbacks[0]()
		if !b.banned || b.unbanCalls != 0 {
			t.Fatalf("%s: stale timed ban lifted the permanent ban", permanent)
		}
	}
}

func TestStaleTimedBanTimerKeepsLongerTimedBan(t *testing.T) {
	d := openTimerTestDB(t)
	s := &recordingScheduler{}
	h := &ModerationHandler{DB: d, Scheduler: s}
	b := newStateBanner()
	for _, dur := range []time.Duration{time.Hour, 24 * time.Hour} {
		if _, _, err := h.TBan(b, "mod", "400", dur, ""); err != nil {
			t.Fatal(err)
		}
	}
	s.callbacks[0]()
	if !b.banned {
		t.Fatal("stale timed ban lifted the newer timed ban")
	}
}

func TestSoftbanBansThenUnbansOnBothPlatforms(t *testing.T) {
	for _, platform := range []string{PlatformDiscord, PlatformTelegram} {
		t.Run(platform, func(t *testing.T) {
			d := openTimerTestDB(t)
			s := &recordingScheduler{}
			h := &ModerationHandler{DB: d, Scheduler: s}
			b := newStateBanner()
			b.platform = platform
			if _, _, err := h.TBan(b, "mod", "400", time.Hour, "earlier"); err != nil {
				t.Fatal(err)
			}
			b.banned = false
			if _, _, err := h.SBan(b, "mod", "400", "reason"); err != nil {
				t.Fatal(err)
			}
			if b.banned || b.banCalls != 2 || b.unbanCalls != 1 || b.mutedUntil != 0 {
				t.Fatalf("softban did not remove the ban immediately: %+v", b)
			}
			b.banned = true
			s.callbacks[0]()
			if !b.banned || b.unbanCalls != 1 {
				t.Fatal("old timer lifted a ban applied after the softban")
			}
		})
	}
}

// Rows written before the fix can still coexist; only the newest one may expire.
func TestRestoredLegacyDuplicateOnlyNewestExpires(t *testing.T) {
	d := openTimerTestDB(t)
	h := &ModerationHandler{DB: d, Scheduler: &recordingScheduler{}}
	b := newStateBanner()
	b.mutedUntil = 1
	oldID, _ := d.AddMute("discord", "300", "400", time.Now().Add(-time.Minute).Unix(), "old")
	newID, _ := d.AddMute("discord", "300", "400", time.Now().Add(time.Hour).Unix(), "new")
	h.ExpireMute(b, oldID, "400")
	if b.unmuteCalls != 0 {
		t.Fatal("restored stale mute lifted the newer mute")
	}
	rows, _ := d.GetPendingMutes()
	if len(rows) != 1 || rows[0].ID != newID {
		t.Fatalf("stale row should be dropped and newer kept: %+v", rows)
	}
}

func TestFailedUnbanIsRetainedAndRetried(t *testing.T) {
	d := openTimerTestDB(t)
	s := &recordingScheduler{}
	h := &ModerationHandler{DB: d, Scheduler: s}
	b := newStateBanner()
	b.unbanErr = errors.New("API unavailable")
	if _, _, err := h.TBan(b, "mod", "400", time.Hour, "one"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(s.callbacks); i++ {
		if i > 2*expiryMaxAttempts {
			t.Fatal("retries are unbounded")
		}
		s.callbacks[i]()
	}
	if b.unbanCalls != expiryMaxAttempts {
		t.Fatalf("unban attempts = %d, want %d", b.unbanCalls, expiryMaxAttempts)
	}
	rows, err := d.GetPendingTimedBans()
	if err != nil || len(rows) != 1 {
		t.Fatalf("failed unban must keep its row for restart: rows=%d err=%v", len(rows), err)
	}

	// A later restart succeeds and clears the row.
	b.unbanErr = nil
	h.ExpireTimedBan(b, rows[0].ID, "400")
	rows, _ = d.GetPendingTimedBans()
	if b.banned || len(rows) != 0 {
		t.Fatalf("successful retry should unban and delete: banned=%v rows=%d", b.banned, len(rows))
	}
}

func TestFailedUnmuteRetrySucceeds(t *testing.T) {
	d := openTimerTestDB(t)
	s := &recordingScheduler{}
	h := &ModerationHandler{DB: d, Scheduler: s}
	b := newStateBanner()
	b.unmuteErr = errors.New("API unavailable")
	if _, _, err := h.Mute(b, "mod", "400", time.Hour, "one"); err != nil {
		t.Fatal(err)
	}
	s.callbacks[0]()
	if len(s.callbacks) != 2 || s.ids[1] != s.ids[0] {
		t.Fatalf("expected one retry under the same timer ID, got %v", s.ids)
	}
	b.unmuteErr = nil
	s.callbacks[1]()
	rows, _ := d.GetPendingMutes()
	if b.mutedUntil != 0 || len(rows) != 0 {
		t.Fatalf("retry should unmute and delete: mutedUntil=%d rows=%d", b.mutedUntil, len(rows))
	}
}

func TestDiscordMuteRejectsDurationBeyondTimeoutLimit(t *testing.T) {
	d := openTimerTestDB(t)
	h := &ModerationHandler{DB: d, Scheduler: &recordingScheduler{}}
	b := newStateBanner()
	resp, _, err := h.Mute(b, "mod", "400", 29*24*time.Hour, "")
	rows, _ := d.GetPendingMutes()
	if err != nil || !strings.Contains(resp, "28 days") || b.mutedUntil != 0 || len(rows) != 0 {
		t.Fatalf("resp=%q err=%v mutedUntil=%d rows=%d", resp, err, b.mutedUntil, len(rows))
	}
}

func TestWarnReportsFailedTimeout(t *testing.T) {
	d := openTimerTestDB(t)
	h := &WarnHandler{DB: d, Moderation: &ModerationHandler{DB: d, Scheduler: &recordingScheduler{}}}
	b := &fakeModerationBanner{platform: "discord", chatID: "300", restrictErr: errors.New("missing permission")}
	text, _, _, err := h.Warn(b, "mod", "400", "reason")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "timed out for") || !strings.Contains(text, "timeout failed") {
		t.Fatalf("response should report the failed timeout: %q", text)
	}
	if count, _ := d.GetWarningCount("discord", "400"); count != 1 {
		t.Fatalf("warning should be kept, count=%d", count)
	}
}

func TestWarnKeepsLongerMute(t *testing.T) {
	d := openTimerTestDB(t)
	h := &WarnHandler{DB: d, Moderation: &ModerationHandler{DB: d, Scheduler: &recordingScheduler{}}}
	b := newStateBanner()
	muteUntil := time.Now().Add(24 * time.Hour).Unix()
	if _, err := d.AddMute("discord", "300", "400", muteUntil, "long"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := h.Warn(b, "mod", "400", "reason"); err != nil {
		t.Fatal(err)
	}
	if b.mutedUntil != muteUntil {
		t.Fatalf("warn timeout changed mute end to %d, want %d", b.mutedUntil, muteUntil)
	}
}
