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
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

func TestAuditLogFormattingPreservesMessageDataAndPermissionChanges(t *testing.T) {
	message := &discordgo.Message{
		Content: "first line\nsecond line",
		Attachments: []*discordgo.MessageAttachment{{
			Filename: "evidence.txt", ContentType: "text/plain", Size: 42, URL: "https://cdn.example/evidence.txt",
		}},
		Embeds:       []*discordgo.MessageEmbed{{Title: "Link title", Description: "Link description"}},
		StickerItems: []*discordgo.StickerItem{{ID: "sticker", Name: "Wave"}},
	}
	payload := renderMessagePayload(message)
	for _, expected := range []string{"first line", "second line", "evidence.txt", "https://cdn.example/evidence.txt", "Link title", "Link description", "Wave"} {
		if !strings.Contains(payload, expected) {
			t.Errorf("message payload omitted %q:\n%s", expected, payload)
		}
	}

	changes := changedOverwrites(
		[]*discordgo.PermissionOverwrite{{ID: "role", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel}},
		[]*discordgo.PermissionOverwrite{{ID: "role", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages, Deny: discordgo.PermissionMentionEveryone}},
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
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/guilds/guild/audit-logs"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"audit_log_entries":[{"target_id":"target","user_id":"moderator","id":"` + auditID + `","action_type":22,"reason":"spam"}],"users":[],"webhooks":[],"integrations":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/channels/"+serverLogChannelID+"/messages"):
			var request struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			logged = request.Content
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"log"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{Config: &config.Config{DiscordGuildID: "guild"}, Logger: zap.NewNop()}
	bot.onGuildBanAddLog(session, &discordgo.GuildBanAdd{GuildID: "guild", User: &discordgo.User{ID: "target"}})

	for _, expected := range []string{"Member banned", "<@target>", "<@moderator>", "spam"} {
		if !strings.Contains(logged, expected) {
			t.Errorf("server log omitted %q: %s", expected, logged)
		}
	}
}
