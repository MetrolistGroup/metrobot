package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/db"
)

type fakeDehoistConfig struct{}

func (fakeDehoistConfig) GetPermaAdminIDs(platform string) []string {
	if platform == "discord" {
		return []string{"admin-1"}
	}
	return nil
}

type fakeDehoistBanner struct {
	platform string
	members  []MemberInfo

	setCalls map[string]string
}

func (b *fakeDehoistBanner) Ban(userID, reason string) error    { return nil }
func (b *fakeDehoistBanner) Unban(userID string) error          { return nil }
func (b *fakeDehoistBanner) DeleteMessages(userID string) error { return nil }
func (b *fakeDehoistBanner) Restrict(userID string, untilDate int64) error {
	return nil
}
func (b *fakeDehoistBanner) Unrestrict(userID string) error { return nil }
func (b *fakeDehoistBanner) SetNickname(userID, nickname string) error {
	if b.setCalls == nil {
		b.setCalls = make(map[string]string)
	}
	b.setCalls[userID] = nickname
	return nil
}
func (b *fakeDehoistBanner) DMUser(userID, message string) error          { return nil }
func (b *fakeDehoistBanner) GetDisplayName(userID string) (string, error) { return "", nil }
func (b *fakeDehoistBanner) GetAllMembers() ([]MemberInfo, error)         { return b.members, nil }
func (b *fakeDehoistBanner) Platform() string                             { return b.platform }
func (b *fakeDehoistBanner) ChatID() string                               { return "test-chat" }

func openModerationTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "moderation-test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database
}

func TestDehoistBulkSkipsAdminsAndBots(t *testing.T) {
	database := openModerationTestDB(t)
	handler := &ModerationHandler{DB: database}

	banner := &fakeDehoistBanner{
		platform: "discord",
		members: []MemberInfo{
			{UserID: "admin-1", Username: "Admin", DisplayName: "!!!Admin"},
			{UserID: "bot-1", Username: "BotUser", DisplayName: "!!!Bot", IsBot: true},
			{UserID: "user-1", Username: "Alice", DisplayName: "!!!Alice"},
			{UserID: "user-2", Username: "Bob", DisplayName: "Bob"},
		},
	}

	resp, err := handler.Dehoist(banner, "", false, fakeDehoistConfig{})
	if err != nil {
		t.Fatalf("Dehoist bulk: %v", err)
	}

	if _, ok := banner.setCalls["admin-1"]; ok {
		t.Fatalf("admin user should not be dehoisted")
	}
	if _, ok := banner.setCalls["bot-1"]; ok {
		t.Fatalf("bot user should not be dehoisted")
	}

	wantNew := stripHoistChars("!!!Alice")
	gotNew, ok := banner.setCalls["user-1"]
	if !ok {
		t.Fatalf("expected user-1 to be dehoisted")
	}
	if gotNew != wantNew {
		t.Fatalf("user-1 new nickname = %q, want %q", gotNew, wantNew)
	}

	if _, ok := banner.setCalls["user-2"]; ok {
		t.Fatalf("user-2 should not be renamed (no hoist chars)")
	}

	wantResp := "Successfully dehoisted 1 members out of 4 server members."
	if resp != wantResp {
		t.Fatalf("response = %q, want %q", resp, wantResp)
	}
}

func TestDehoistSkipsAdminTarget(t *testing.T) {
	database := openModerationTestDB(t)
	handler := &ModerationHandler{DB: database}

	banner := &fakeDehoistBanner{
		platform: "discord",
	}

	resp, err := handler.Dehoist(banner, "admin-1", false, fakeDehoistConfig{})
	if err != nil {
		t.Fatalf("Dehoist admin: %v", err)
	}

	if len(banner.setCalls) != 0 {
		t.Fatalf("no nicknames should be changed for admin target, got: %#v", banner.setCalls)
	}

	if !strings.Contains(resp, "admin") {
		t.Fatalf("response should explain admin is not dehoisted, got: %q", resp)
	}
}
