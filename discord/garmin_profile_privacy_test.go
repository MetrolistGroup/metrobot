package discord

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/bwmarrin/discordgo"
)

func TestGetGarminDiscordProfileUsesOnlyDiscordData(t *testing.T) {
	const targetID = "123456789012345678"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/members/"):
			_, _ = w.Write([]byte(`{"user":{"id":"123456789012345678","username":"target"},"roles":["pronouns"]}`))
		case strings.HasSuffix(r.URL.Path, "/roles"):
			_, _ = w.Write([]byte(`[{"id":"pronouns","name":"she/her"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{Config: &config.Config{DiscordGuildID: "guild"}}

	profile, err := bot.getGarminDiscordProfile(session, targetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"target", "she/her", "server roles", `"bio":null`} {
		if !strings.Contains(profile, expected) {
			t.Errorf("Discord profile missing %q: %s", expected, profile)
		}
	}
	for _, removed := range []string{"remembered_info", "user-saved", "personalization"} {
		if strings.Contains(profile, removed) {
			t.Errorf("Discord profile contains removed field %q: %s", removed, profile)
		}
	}
}
