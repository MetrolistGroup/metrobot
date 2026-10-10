package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

func TestAuditLogFormattingPreservesMessageDataAndPermissionChanges(t *testing.T) {
	message := &discord.Message{
		Content: "first line\nsecond line",
		Attachments: []discord.Attachment{{
			Filename: "evidence.txt", ContentType: ptr("text/plain"), Size: 42, URL: "https://cdn.example/evidence.txt",
		}},
		Embeds:       []discord.Embed{{Title: "Link title", Description: "Link description"}},
		StickerItems: []discord.MessageSticker{{ID: snowflakeID("100000000000000050"), Name: "Wave"}},
	}
	payload := renderMessagePayload(message)
	for _, expected := range []string{"first line", "second line", "evidence.txt", "https://cdn.example/evidence.txt", "Link title", "Link description", "Wave"} {
		if !strings.Contains(payload, expected) {
			t.Errorf("message payload omitted %q:\n%s", expected, payload)
		}
	}

	changes := changedOverwrites(
		[]discord.PermissionOverwrite{discord.RolePermissionOverwrite{RoleID: snowflakeID("100000000000000041"), Allow: discord.PermissionViewChannel}},
		[]discord.PermissionOverwrite{discord.RolePermissionOverwrite{RoleID: snowflakeID("100000000000000041"), Allow: discord.PermissionViewChannel | discord.PermissionSendMessages, Deny: discord.PermissionMentionEveryone}},
	)
	if len(changes) != 1 {
		t.Fatalf("overwrite changes = %d, want 1", len(changes))
	}
	formatted := formatOverwriteChange(changes[0])
	for _, expected := range []string{"Allowed added", "Send Messages", "Denied added", "Mention Everyone"} {
		if !strings.Contains(formatted, expected) {
			t.Errorf("permission change omitted %q: %s", expected, formatted)
		}
	}
}

func TestBanEventUsesAuditActorAndServerLogChannel(t *testing.T) {
	auditID := strconv.FormatInt((time.Now().UnixMilli()-1420070400000)<<22, 10)
	var logged string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/guilds/100000000000000012/audit-logs"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"audit_log_entries":[{"target_id":"100000000000000052","user_id":"100000000000000024","id":"` + auditID + `","action_type":22,"reason":"spam"}],"users":[],"webhooks":[],"integrations":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/channels/"+serverLogChannelID+"/messages"):
			var request struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			logged = request.Content
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"100000000000000019"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Rest = testREST(server.Client())
	session.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: session.Rest.HTTPClient().Transport, target: server.URL}
	bot := &Bot{Config: &config.Config{DiscordGuildID: "100000000000000012"}, Logger: zap.NewNop()}
	bot.onGuildBanAddLog(session, &events.GuildBan{GenericGuild: &events.GenericGuild{GuildID: snowflakeID("100000000000000012")},
		User: discord.User{ID: snowflakeID("100000000000000052")}})

	for _, expected := range []string{"Member banned", "<@100000000000000052>", "<@100000000000000024>", "spam"} {
		if !strings.Contains(logged, expected) {
			t.Errorf("server log omitted %q: %s", expected, logged)
		}
	}
}
