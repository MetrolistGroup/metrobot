package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

func TestGarminKillRequiresStaffReplyAndEliminatesTarget(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/guilds/guild/members/target"):
			var payload struct {
				Until time.Time `json:"communication_disabled_until"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode timeout request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if remaining := time.Until(payload.Until); remaining < 25*time.Second || remaining > 35*time.Second {
				t.Errorf("timeout duration = %s, want about 30s", remaining)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/channels/channel/messages/target-message"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/channel/messages"):
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode response request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if payload.Content != "Target eliminated." {
				t.Errorf("reply = %q", payload.Content)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"reply","channel_id":"channel"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
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
	session.State.User = &discordgo.User{ID: "bot"}
	if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild", Roles: []*discordgo.Role{
		{ID: "guild"},
		{ID: "staff", Permissions: discordgo.PermissionModerateMembers},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.State.ChannelAdd(&discordgo.Channel{ID: "channel", GuildID: "guild"}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []*discordgo.Member{
		{GuildID: "guild", User: &discordgo.User{ID: "staff"}, Roles: []string{"staff"}},
		{GuildID: "guild", User: &discordgo.User{ID: "user"}},
	} {
		if err := session.State.MemberAdd(member); err != nil {
			t.Fatal(err)
		}
	}

	bot := &Bot{Config: &config.Config{DiscordGuildID: "guild"}, Logger: zap.NewNop()}
	message := func(author string, reply bool) *discordgo.MessageCreate {
		m := &discordgo.Message{ID: "command", GuildID: "guild", ChannelID: "channel", Content: "ok garmin kill", Author: &discordgo.User{ID: author}}
		if reply {
			m.ReferencedMessage = &discordgo.Message{ID: "target-message", Author: &discordgo.User{ID: "target"}}
		}
		return &discordgo.MessageCreate{Message: m}
	}

	bot.onMessageCreate(session, message("staff", true))
	if len(requests) != 3 {
		t.Fatalf("staff reply made %d requests, want timeout, deletion, and response: %v", len(requests), requests)
	}
	bot.onMessageCreate(session, message("user", true))
	bot.onMessageCreate(session, message("staff", false))
	if len(requests) != 3 {
		t.Fatalf("unauthorized or non-reply command made requests: %v", requests[3:])
	}
}
