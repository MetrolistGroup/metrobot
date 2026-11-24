package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

type heldTimeouts struct{ callbacks []func() }

func (h *heldTimeouts) AddTimer(_ string, _ time.Duration, callback func()) {
	h.callbacks = append(h.callbacks, callback)
}

func TestGarminKillPreservesLongerStoredMute(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "kill.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	client := mustTestClient(t)
	var timeouts []time.Time
	authority := testGuildAuthority{guildID: "300", ownerID: "999", roles: []testRole{{id: "60", permissions: discord.PermissionModerateMembers, position: 5}, {id: "61", position: 1}, {id: "62", permissions: discord.PermissionModerateMembers, position: 8}}, members: map[string][]string{"500": {"60"}, "400": {"61"}, testBotUserID: {"62"}}}
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if lookup, ok := authority.response(r); ok {
			return lookup, nil
		}
		body := `{"id":"800","channel_id":"200"}`
		switch r.Method + " " + r.URL.Path {
		case "PATCH /api/v10/guilds/300/members/400":
			var payload struct {
				Until time.Time `json:"communication_disabled_until"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			timeouts = append(timeouts, payload.Until)
		case "DELETE /api/v10/channels/200/messages/110", "POST /api/v10/channels/200/messages":
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	timers := &heldTimeouts{}
	moderation := &cmd.ModerationHandler{DB: database, Scheduler: timers}
	b := &Bot{Client: client, Config: testConfig(config.Config{DiscordGuildID: "300"}), DB: database, Logger: zap.NewNop(), Moderation: moderation}
	if _, _, err := moderation.Mute(b.newBanner(), "500", "400", time.Hour, "long mute"); err != nil {
		t.Fatal(err)
	}
	b.handleGarminKill(client, &discord.Message{
		ID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Author: discord.User{ID: 500},
		ReferencedMessage: &discord.Message{ID: 110, Author: discord.User{ID: 400}},
	})
	if len(timeouts) != 2 || !timeouts[1].Equal(timeouts[0]) || time.Until(timeouts[1]) < 59*time.Minute || len(timers.callbacks) != 2 {
		t.Fatalf("kill shortened the mute or bypassed guarded expiry: timeouts=%v timers=%d", timeouts, len(timers.callbacks))
	}
	timers.callbacks[0]()
	rows, err := database.GetPendingMutes()
	if err != nil || len(rows) != 1 || rows[0].ID != 2 || len(timeouts) != 2 {
		t.Fatalf("old callback changed the newer mute: rows=%+v err=%v timeouts=%v", rows, err, timeouts)
	}
}
