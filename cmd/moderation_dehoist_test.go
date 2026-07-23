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
func (b *fakeDehoistBanner) GetUsername(userID string) (string, error)    { return "testuser", nil }
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

func TestDehoistBulkUsesOriginalNamesAndSkipsProtectedMembers(t *testing.T) {
	database := openModerationTestDB(t)
	handler := &ModerationHandler{DB: database}

	banner := &fakeDehoistBanner{
		platform: "discord",
		members: []MemberInfo{
			{UserID: "admin-1", OriginalName: "!!!Admin"},
			{UserID: "bot-1", OriginalName: "!!!Bot", IsBot: true},
			{UserID: "user-1", OriginalName: "!!!𝔄𝔩𝔦𝔠𝔢", Nickname: "change your display name"},
			{UserID: "user-2", OriginalName: "Bob"},
			{UserID: "user-3", OriginalName: "!!!Ⓐⓛⓘⓒⓔ", Nickname: "Chosen Name"},
			{UserID: "user-4", OriginalName: "!!!Ａｌｉｃｅ"},
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

	wantNew := stripHoistChars("!!!𝔄𝔩𝔦𝔠𝔢")
	gotNew, ok := banner.setCalls["user-1"]
	if !ok {
		t.Fatalf("expected legacy nickname for user-1 to be migrated")
	}
	if gotNew != wantNew {
		t.Fatalf("user-1 new nickname = %q, want %q", gotNew, wantNew)
	}

	if _, ok := banner.setCalls["user-2"]; ok {
		t.Fatalf("user-2 should not be renamed (no hoist chars)")
	}
	if _, ok := banner.setCalls["user-3"]; ok {
		t.Fatalf("user-3 should not have their manual nickname replaced")
	}
	if got := banner.setCalls["user-4"]; got != "alice" {
		t.Fatalf("user-4 new nickname = %q, want %q", got, "alice")
	}

	wantResp := "Successfully dehoisted 2 members out of 6 server members."
	if resp != wantResp {
		t.Fatalf("response = %q, want %q", resp, wantResp)
	}
}

func TestDehoistBulkDryRunDoesNotChangeNicknames(t *testing.T) {
	database := openModerationTestDB(t)
	handler := &ModerationHandler{DB: database}
	banner := &fakeDehoistBanner{
		platform: "discord",
		members: []MemberInfo{
			{UserID: "legacy", OriginalName: "!!!Ⓐⓛⓘⓒⓔ", Nickname: "change your display name"},
			{UserID: "manual", OriginalName: "!!!Ⓑⓞⓑ", Nickname: "Robert"},
		},
	}

	resp, err := handler.Dehoist(banner, "", true, fakeDehoistConfig{})
	if err != nil {
		t.Fatalf("Dehoist dry run: %v", err)
	}
	if len(banner.setCalls) != 0 {
		t.Fatalf("dry run changed nicknames: %#v", banner.setCalls)
	}
	if !strings.Contains(resp, "change your display name → alice") {
		t.Fatalf("dry-run response does not contain legacy migration: %q", resp)
	}
	if strings.Contains(resp, "Robert") {
		t.Fatalf("dry-run response contains a manual nickname: %q", resp)
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

func TestNeedsDehoistingMatchesStripHoistChars(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "Alice", want: false},
		{name: "!!!Alice", want: true},
		{name: " Alice", want: true},
		{name: "Alice!", want: true},
		{name: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsDehoisting(tt.name); got != tt.want {
				t.Fatalf("NeedsDehoisting(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestStripHoistCharsNormalizesUnicode(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "!!!𝔄𝔩𝔦𝔠𝔢", want: "alice"},
		{name: "Ａｌｉｃｅ", want: "alice"},
		{name: "Ⓐⓛⓘⓒⓔ", want: "alice"},
		{name: "Áłïçé", want: "alice"},
		{name: "A̴͗̽l̷i̸c̵e̵", want: "Alice"},
		{name: "꧁Alice꧂", want: "Alice"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripHoistChars(tt.name); got != tt.want {
				t.Fatalf("stripHoistChars(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
