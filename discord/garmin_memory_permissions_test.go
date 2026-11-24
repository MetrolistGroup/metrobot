package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestSharedMemoryRequiresNativeManageServer(t *testing.T) {
	// 56 is a legacy bot admin without Discord permissions, 57 has Manage Server, 99 owns the server.
	allowed := map[string]bool{"56": false, "57": true, "99": true, "58": false}
	for caller, canManage := range allowed {
		for _, action := range []string{"append", "replace", "clear", "view"} {
			t.Run(caller+"/"+action, func(t *testing.T) {
				database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { database.Close() })
				if err := database.AddAdmin("discord", "56", "owner"); err != nil {
					t.Fatal(err)
				}
				memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
				if err != nil {
					t.Fatal(err)
				}
				if err := memory.Append("existing approved fact"); err != nil {
					t.Fatal(err)
				}
				before, err := memory.Read()
				if err != nil {
					t.Fatal(err)
				}
				authority := testGuildAuthority{guildID: "12", ownerID: "99", roles: []testRole{{id: "80", permissions: discord.PermissionManageGuild, position: 3}}, members: map[string][]string{"56": {}, "57": {"80"}}}
				b := &Bot{DB: database, Config: testConfig(config.Config{DiscordGuildID: "12"}), garminMemory: memory, Logger: zap.NewNop()}
				client := mustTestClient(t)
				var response discord.MessageCreate
				acknowledged := false
				client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					if lookup, ok := authority.response(r); ok {
						if !acknowledged {
							t.Error("memory permission lookup ran before acknowledgement")
						}
						return lookup, nil
					}
					if r.Method == http.MethodPost {
						acknowledged = true
						return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
					}
					if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
						t.Fatal(err)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"18","channel_id":"5"}`)), Request: r}, nil
				})})
				interaction := testCommandInteraction(t, client, fmt.Sprintf(`{"id":"17","application_id":"1","token":"test","type":2,"data":{"name":"memory","type":1,"options":[{"name":%q,"type":1,"options":[{"name":"content","type":3,"value":"new approved fact"}]}]}}`, action))
				b.handleGarminMemory(client, interaction, interaction.SlashCommandInteractionData(), caller)
				after, err := memory.Read()
				if err != nil {
					t.Fatal(err)
				}
				switch {
				case !canManage:
					if after != before || !strings.Contains(response.Content, "permission") {
						t.Fatalf("caller without Manage Server used memory: memory=%q response=%q", after, response.Content)
					}
				case action == "view":
					if after != before || !strings.Contains(response.Content, "existing approved fact") {
						t.Fatalf("manager read changed: memory=%q response=%q", after, response.Content)
					}
				default:
					if after == before || !strings.Contains(response.Content, "memory updated") {
						t.Fatalf("authorized write failed: memory=%q response=%q", after, response.Content)
					}
				}
			})
		}
	}
}
