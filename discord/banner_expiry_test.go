package discord

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"go.uber.org/zap"
)

func testBannerReplying(t *testing.T, status int, body string) *DiscordBanner {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	return &DiscordBanner{session: client, guildID: "300", logger: zap.NewNop()}
}

func TestBannerLiftTreatsOnlyAbsentTargetAsDone(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		body    string
		lift    func(*DiscordBanner) error
		wantErr bool
	}{
		{"unknown ban", 404, `{"code":10026,"message":"Unknown Ban"}`, func(b *DiscordBanner) error { return b.Unban("400") }, false},
		{"unban unknown guild", 404, `{"code":10004,"message":"Unknown Guild"}`, func(b *DiscordBanner) error { return b.Unban("400") }, true},
		{"unban missing permission", 403, `{"code":50013,"message":"Missing Permissions"}`, func(b *DiscordBanner) error { return b.Unban("400") }, true},
		{"unban unknown member is not a ban miss", 404, `{"code":10007,"message":"Unknown Member"}`, func(b *DiscordBanner) error { return b.Unban("400") }, true},
		{"unknown member", 404, `{"code":10007,"message":"Unknown Member"}`, func(b *DiscordBanner) error { return b.Unrestrict("400") }, false},
		{"unrestrict unknown guild", 404, `{"code":10004,"message":"Unknown Guild"}`, func(b *DiscordBanner) error { return b.Unrestrict("400") }, true},
		{"unrestrict missing permission", 403, `{"code":50013,"message":"Missing Permissions"}`, func(b *DiscordBanner) error { return b.Unrestrict("400") }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.lift(testBannerReplying(t, tt.status, tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

type noTimers struct{}

func (noTimers) AddTimer(string, time.Duration, func()) {}

func TestExpiryDropsRowsForAlreadyLiftedPunishment(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	h := &cmd.ModerationHandler{DB: database, Scheduler: noTimers{}}
	banID, err := database.AddTimedBan("discord", "300", "400", time.Now().Unix(), "")
	if err != nil {
		t.Fatal(err)
	}
	muteID, err := database.AddMute("discord", "300", "400", time.Now().Unix(), "")
	if err != nil {
		t.Fatal(err)
	}
	h.ExpireTimedBan(testBannerReplying(t, 404, `{"code":10026,"message":"Unknown Ban"}`), banID, "400")
	h.ExpireMute(testBannerReplying(t, 404, `{"code":10007,"message":"Unknown Member"}`), muteID, "400")
	bans, _ := database.GetPendingTimedBans()
	mutes, _ := database.GetPendingMutes()
	if len(bans) != 0 || len(mutes) != 0 {
		t.Fatalf("rows kept after absent-target expiry: bans=%d mutes=%d", len(bans), len(mutes))
	}
}
