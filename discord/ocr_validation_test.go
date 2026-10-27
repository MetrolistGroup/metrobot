package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestOCRValidationBeforeProviderCall(t *testing.T) {
	for _, mode := range []string{"empty", "not configured", "too many images"} {
		t.Run(mode, func(t *testing.T) {
			client := mustTestClient(t)
			var response struct {
				Type discord.InteractionResponseType `json:"type"`
				Data discord.MessageCreate           `json:"data"`
			}
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/callback") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
					t.Error(err)
				}
				return &http.Response{StatusCode: 204, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})})
			message := map[string]any{"id": "50", "content": "", "author": map[string]string{"id": "400"}}
			want := "Select a message"
			if mode == "not configured" {
				message["content"] = "hola"
				want = "isn't configured"
			}
			if mode == "too many images" {
				var attachments []map[string]string
				for _, filename := range []string{"a.png", "b.png", "c.png", "d.png"} {
					attachments = append(attachments, map[string]string{"filename": filename, "url": "https://cdn.discordapp.com/" + filename})
				}
				message["attachments"] = attachments
				want = "at most three"
			}
			encoded, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			interaction := testCommandInteraction(t, client, `{
				"id":"100","application_id":"123456789012345678","token":"token","type":2,
				"guild_id":"300","channel":{"id":"200","type":0},"member":{"user":{"id":"500"}},
				"data":{"name":"OCR Translate","type":3,"target_id":"50","resolved":{"messages":{"50":`+string(encoded)+`}}}
			}`)
			b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop()}
			b.onInteractionCreate(client, interaction)
			data, err := json.Marshal(response.Data)
			if err != nil {
				t.Fatal(err)
			}
			if response.Type != discord.InteractionResponseTypeCreateMessage || !response.Data.Flags.Has(discord.MessageFlagEphemeral) || !strings.Contains(string(data), want) {
				t.Fatalf("validation response = %s, want %q", data, want)
			}
		})
	}
}
