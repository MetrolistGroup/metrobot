package discord

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
)

func TestGetGarminDiscordProfileKeepsSavedFieldsPrivate(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const targetID = "123456789012345678"
	if err := database.SetGarminMemoryConsent("discord", targetID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.SetGarminUserMemory("discord", targetID, db.GarminUserMemory{
		Info: "saved-secret", Pronouns: "xe/xem", Bio: "cat bio",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.AddAdmin("discord", "admin", "owner"); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/members/"):
			_, _ = w.Write([]byte(`{"user":{"id":"123456789012345678","username":"target"},"roles":[]}`))
		case strings.HasSuffix(r.URL.Path, "/roles"):
			_, _ = w.Write([]byte(`[]`))
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
	bot := &Bot{DB: database, Config: &config.Config{DiscordGuildID: "guild"}}

	outsider, err := bot.getGarminDiscordProfile(session, "outsider", targetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"saved-secret", "xe/xem", "cat bio"} {
		if strings.Contains(outsider, private) {
			t.Errorf("outsider profile exposed %q: %s", private, outsider)
		}
	}
	for _, requesterID := range []string{targetID, "admin"} {
		profile, err := bot.getGarminDiscordProfile(session, requesterID, targetID)
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"saved-secret", "xe/xem", "cat bio"} {
			if !strings.Contains(profile, expected) {
				t.Errorf("profile for %s missing %q: %s", requesterID, expected, profile)
			}
		}
	}
}
