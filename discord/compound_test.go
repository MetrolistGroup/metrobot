package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestCompoundSigmaCommands(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SetGSMArenaCache("wolfram:191cm to feet and inches", []byte("6 feet 3.2 inches")); err != nil {
		t.Fatal(err)
	}

	var replies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v10/channels/5/messages" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var message discord.MessageCreate
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Error(err)
		}
		if message.MessageReference == nil || message.MessageReference.MessageID == nil || *message.MessageReference.MessageID != 23 {
			t.Errorf("reply lost original message reference: %+v", message.MessageReference)
		}
		if message.AllowedMentions == nil || len(message.AllowedMentions.Parse) != 0 || len(message.AllowedMentions.Users) != 0 || len(message.AllowedMentions.Roles) != 0 || message.AllowedMentions.RepliedUser {
			t.Errorf("reply allows mentions: %+v", message.AllowedMentions)
		}
		replies = append(replies, message.Content)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"42","channel_id":"5"}`)
	}))
	defer server.Close()
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	b := &Bot{DB: database, Config: testConfig(config.Config{DiscordGuildID: "12", WolframAppIDs: []string{"unused"}}), Logger: zap.NewNop()}

	for _, test := range []struct {
		name, content string
		want          []string
	}{
		{"height example", "im actually pretty tall {{>>wa 191cm to feet and inches}} so i am defo not 3ft tall", []string{"6 feet 3.2 inches"}},
		{"multiple blocks", "first {{>>wa 2+2}} then {{>>wa 3+5}} done", []string{"4", "8"}},
		{"whitespace", "before {{ \n >>WA 2+2 \n }} after", []string{"4"}},
		{"unknown block", "{{>>unknown}} then {{>>wa 2+2}}", []string{"4"}},
		{"incomplete trailing block", "{{>>wa 2+2}} and {{>>wa 3+5", []string{"4"}},
		{"another command", "a reminder {{>>rmme}} please", []string{"Usage: `>>rmme <when> [text]` (e.g. `>>rmme 45m pizza out`, `>>rmme 1h30m call mom`, `>>rmme 2d`)"}},
		{"standalone unchanged", ">>wa 2+2", []string{"4"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			replies = nil
			message := &discord.Message{ID: 23, ChannelID: 5, GuildID: ptr(snowflake.ID(12)), Author: discord.User{ID: 56}, Content: test.content}
			b.onMessageCreate(client, message)
			if !slices.Equal(replies, test.want) {
				t.Errorf("replies = %q, want %q", replies, test.want)
			}
			if message.Content != test.content {
				t.Errorf("original message changed to %q", message.Content)
			}
		})
	}
}

func TestCompoundSigmaCommandsIgnoreNonCommands(t *testing.T) {
	for _, content := range []string{
		"ordinary text", "a bare >>wa 2+2 in prose", "{{}}", "{{not a command}}",
		"{{>>unknown}}", "{{>>wa 2+2}", "{>>wa 2+2}}", "{{>>wa 2+2",
	} {
		if testBot().handleCompoundSigmaCommands(nil, &discord.Message{}, content) {
			t.Errorf("handled non-command %q", content)
		}
	}
}
