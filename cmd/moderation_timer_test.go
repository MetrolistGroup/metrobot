package cmd

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

type fakeModerationConfig struct{}

func (fakeModerationConfig) GetPermaAdminIDs(platform string) []string { return nil }

type fakeModerationBanner struct {
	platform string
	chatID   string

	banErr      error
	restrictErr error
	unbanCalls  int
	unmuteCalls int
}

func (b *fakeModerationBanner) Ban(userID, reason string) error { return b.banErr }
func (b *fakeModerationBanner) Unban(userID string) error {
	b.unbanCalls++
	return nil
}
func (b *fakeModerationBanner) DeleteMessages(userID string) error { return nil }
func (b *fakeModerationBanner) Restrict(userID string, untilDate int64) error {
	return b.restrictErr
}
func (b *fakeModerationBanner) Unrestrict(userID string) error {
	b.unmuteCalls++
	return nil
}
func (b *fakeModerationBanner) SetNickname(userID, nickname string) error    { return nil }
func (b *fakeModerationBanner) DMUser(userID, message string) error          { return nil }
func (b *fakeModerationBanner) GetDisplayName(userID string) (string, error) { return "", nil }
func (b *fakeModerationBanner) GetUsername(userID string) (string, error)    { return "target", nil }
func (b *fakeModerationBanner) GetAllMembers() ([]MemberInfo, error)         { return nil, nil }
func (b *fakeModerationBanner) Platform() string                             { return b.platform }
func (b *fakeModerationBanner) ChatID() string                               { return b.chatID }

type fakeScheduler struct {
	id       string
	duration time.Duration
	callback func()
}

func (s *fakeScheduler) AddTimer(id string, duration time.Duration, cleanupCallback func()) {
	s.id = id
	s.duration = duration
	s.callback = cleanupCallback
}

func openTimerTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "moderation-timer-test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestTBanSchedulesManagedTimerAndDeletesOnExpiry(t *testing.T) {
	database := openTimerTestDB(t)
	scheduler := &fakeScheduler{}
	handler := &ModerationHandler{DB: database, Scheduler: scheduler}
	banner := &fakeModerationBanner{platform: "discord", chatID: "guild"}

	if _, _, err := handler.TBan(banner, "mod", "target", time.Hour, "reason", fakeModerationConfig{}); err != nil {
		t.Fatalf("TBan: %v", err)
	}
	if scheduler.id != "ban_1" || scheduler.duration != time.Hour || scheduler.callback == nil {
		t.Fatalf("timer not scheduled as expected: %#v", scheduler)
	}

	bans, err := database.GetPendingTimedBans()
	if err != nil {
		t.Fatalf("GetPendingTimedBans: %v", err)
	}
	if len(bans) != 1 {
		t.Fatalf("expected 1 timed ban, got %d", len(bans))
	}

	scheduler.callback()
	if banner.unbanCalls != 1 {
		t.Fatalf("expected unban callback, got %d calls", banner.unbanCalls)
	}
	bans, err = database.GetPendingTimedBans()
	if err != nil {
		t.Fatalf("GetPendingTimedBans after callback: %v", err)
	}
	if len(bans) != 0 {
		t.Fatalf("timed ban should be deleted after callback, got %d", len(bans))
	}
}

func TestTBanRollsBackStoredBanWhenBanFails(t *testing.T) {
	database := openTimerTestDB(t)
	handler := &ModerationHandler{DB: database, Scheduler: &fakeScheduler{}}
	banner := &fakeModerationBanner{platform: "discord", chatID: "guild", banErr: errors.New("api failed")}

	if _, _, err := handler.TBan(banner, "mod", "target", time.Hour, "reason", fakeModerationConfig{}); err == nil {
		t.Fatal("TBan should fail when platform ban fails")
	}
	bans, err := database.GetPendingTimedBans()
	if err != nil {
		t.Fatalf("GetPendingTimedBans: %v", err)
	}
	if len(bans) != 0 {
		t.Fatalf("timed ban should have been rolled back, got %d", len(bans))
	}
}

func TestMuteSchedulesManagedTimerAndUnrestrictsOnExpiry(t *testing.T) {
	database := openTimerTestDB(t)
	scheduler := &fakeScheduler{}
	handler := &ModerationHandler{DB: database, Scheduler: scheduler}
	banner := &fakeModerationBanner{platform: "discord", chatID: "guild"}

	if _, _, err := handler.Mute(banner, "mod", "target", time.Hour, "reason", fakeModerationConfig{}); err != nil {
		t.Fatalf("Mute: %v", err)
	}
	if scheduler.id != "mute_1" || scheduler.duration != time.Hour || scheduler.callback == nil {
		t.Fatalf("timer not scheduled as expected: %#v", scheduler)
	}

	scheduler.callback()
	if banner.unmuteCalls != 1 {
		t.Fatalf("expected unrestrict callback, got %d calls", banner.unmuteCalls)
	}
	mutes, err := database.GetPendingMutes()
	if err != nil {
		t.Fatalf("GetPendingMutes: %v", err)
	}
	if len(mutes) != 0 {
		t.Fatalf("mute should be deleted after callback, got %d", len(mutes))
	}
}

func TestMuteRollsBackStoredMuteWhenRestrictFails(t *testing.T) {
	database := openTimerTestDB(t)
	handler := &ModerationHandler{DB: database, Scheduler: &fakeScheduler{}}
	banner := &fakeModerationBanner{platform: "discord", chatID: "guild", restrictErr: errors.New("api failed")}

	if _, _, err := handler.Mute(banner, "mod", "target", time.Hour, "reason", fakeModerationConfig{}); err == nil {
		t.Fatal("Mute should fail when platform restrict fails")
	}
	mutes, err := database.GetPendingMutes()
	if err != nil {
		t.Fatalf("GetPendingMutes: %v", err)
	}
	if len(mutes) != 0 {
		t.Fatalf("mute should have been rolled back, got %d", len(mutes))
	}
}
