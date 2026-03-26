package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/db"
)

type fakeWarnConfig struct{}

func (fakeWarnConfig) GetPermaAdminIDs(platform string) []string { return nil }

type fakeWarnBanner struct {
	platform string
	chatID   string
	banCalls int
}

func (b *fakeWarnBanner) Ban(userID, reason string) error    { b.banCalls++; return nil }
func (b *fakeWarnBanner) Unban(userID string) error          { return nil }
func (b *fakeWarnBanner) DeleteMessages(userID string) error { return nil }
func (b *fakeWarnBanner) Restrict(userID string, untilDate int64) error {
	return nil
}
func (b *fakeWarnBanner) Unrestrict(userID string) error               { return nil }
func (b *fakeWarnBanner) SetNickname(userID, nickname string) error    { return nil }
func (b *fakeWarnBanner) DMUser(userID, message string) error          { return nil }
func (b *fakeWarnBanner) GetDisplayName(userID string) (string, error) { return "", nil }
func (b *fakeWarnBanner) GetUsername(userID string) (string, error)    { return "testuser", nil }
func (b *fakeWarnBanner) GetAllMembers() ([]MemberInfo, error)         { return nil, nil }
func (b *fakeWarnBanner) Platform() string                             { return b.platform }
func (b *fakeWarnBanner) ChatID() string                               { return b.chatID }

func TestWarningsAreOneIndexed(t *testing.T) {
	database := openWarnTestDB(t)
	handler := &WarnHandler{DB: database}
	banner := &fakeWarnBanner{platform: "discord", chatID: "123"}

	if _, err := database.AddWarning("discord", "target", "first", "mod-1"); err != nil {
		t.Fatalf("AddWarning first: %v", err)
	}
	if _, err := database.AddWarning("discord", "target", "second", "mod-2"); err != nil {
		t.Fatalf("AddWarning second: %v", err)
	}

	got, err := handler.Warnings(banner, "target")
	if err != nil {
		t.Fatalf("Warnings: %v", err)
	}

	if !strings.Contains(got, "[1] first") || !strings.Contains(got, "[2] second") {
		t.Fatalf("Warnings() output not 1-indexed: %q", got)
	}
}

func TestUnwarnUsesOneBasedIDs(t *testing.T) {
	database := openWarnTestDB(t)
	handler := &WarnHandler{DB: database}
	banner := &fakeWarnBanner{platform: "discord", chatID: "123"}

	if _, err := database.AddWarning("discord", "target", "first", "mod-1"); err != nil {
		t.Fatalf("AddWarning first: %v", err)
	}
	if _, err := database.AddWarning("discord", "target", "second", "mod-2"); err != nil {
		t.Fatalf("AddWarning second: %v", err)
	}

	resp, _, err := handler.Unwarn("discord", "caller", "target", 1, nil)
	if err != nil {
		t.Fatalf("Unwarn: %v", err)
	}
	if !strings.Contains(resp, "Warning #1 removed") {
		t.Fatalf("Unwarn() response = %q", resp)
	}

	got, err := handler.Warnings(banner, "target")
	if err != nil {
		t.Fatalf("Warnings after unwarn: %v", err)
	}
	if strings.Contains(got, "[1] first") {
		t.Fatalf("first warning should have been removed: %q", got)
	}
	if !strings.Contains(got, "[1] second") {
		t.Fatalf("remaining warning should be renumbered to 1: %q", got)
	}

	if _, _, err := handler.Unwarn("discord", "caller", "target", 0, nil); err == nil {
		t.Fatal("Unwarn() with id 0 should fail")
	}
}

func TestWarnThresholdReturnsSingleCombinedMessage(t *testing.T) {
	database := openWarnTestDB(t)
	handler := &WarnHandler{DB: database}
	banner := &fakeWarnBanner{platform: "discord", chatID: "test-chat"}

	for i := 0; i < 2; i++ {
		if _, _, _, err := handler.Warn(banner, "mod", "target", "reason", fakeWarnConfig{}); err != nil {
			t.Fatalf("Warn pre-threshold #%d: %v", i+1, err)
		}
	}

	resp, extras, _, err := handler.Warn(banner, "mod", "target", "third reason", fakeWarnConfig{})
	if err != nil {
		t.Fatalf("Warn threshold: %v", err)
	}

	if !strings.Contains(resp, "(3/3)") {
		t.Fatalf("response should include third warning count, got: %q", resp)
	}
	if !strings.Contains(resp, "Auto-action: permanently banned") {
		t.Fatalf("response should include auto-ban notice, got: %q", resp)
	}
	if len(extras) != 0 {
		t.Fatalf("expected no extra messages, got: %#v", extras)
	}
	if banner.banCalls != 1 {
		t.Fatalf("expected one auto-ban call, got %d", banner.banCalls)
	}
}

func openWarnTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "warn-test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database
}
