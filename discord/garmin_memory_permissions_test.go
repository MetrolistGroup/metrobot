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
	"go.uber.org/zap"
)

func TestSharedMemoryWritesRequireOwnerEvenForAdmins(t *testing.T) {
	for _, caller := range []string{"100000000000000056", garminNyxID, garminLampID} {
		for _, action := range []string{"append", "replace", "clear", "view"} {
			t.Run(caller+"/"+action, func(t *testing.T) {
				database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { database.Close() })
				if err := database.AddAdmin("discord", caller, garminNyxID); err != nil {
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
				b := &Bot{DB: database, Config: &config.Config{}, garminMemory: memory, Logger: zap.NewNop()}
				client := mustTestClient(t)
				var response struct {
					Data struct {
						Content string `json:"content"`
					}
				}
				client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
						t.Fatal(err)
					}
					return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				})})
				interaction := testCommandInteraction(t, client, fmt.Sprintf(`{"id":"17","application_id":"1","token":"test","type":2,"data":{"name":"memory","type":1,"options":[{"name":%q,"type":1,"options":[{"name":"content","type":3,"value":"new approved fact"}]}]}}`, action))
				b.handleGarminMemory(client, interaction, interaction.SlashCommandInteractionData(), caller)
				after, err := memory.Read()
				if err != nil {
					t.Fatal(err)
				}
				if action == "view" {
					if after != before || !strings.Contains(response.Data.Content, "existing approved fact") {
						t.Fatalf("admin read changed: memory=%q response=%q", after, response.Data.Content)
					}
				} else if !isGarminOwner(caller) {
					if after != before || !strings.Contains(response.Data.Content, "Only Nyx and Lamp") {
						t.Fatalf("non-owner changed memory: %q response=%q", after, response.Data.Content)
					}
				} else if after == before || !strings.Contains(response.Data.Content, "memory updated") {
					t.Fatalf("authorized owner write failed: memory=%q response=%q", after, response.Data.Content)
				}
			})
		}
	}
}
