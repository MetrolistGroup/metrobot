package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
)

func TestDeferredResponsesUseOnlySupportedFlags(t *testing.T) {
	for _, test := range []struct {
		name      string
		ephemeral bool
		flags     discord.MessageFlags
	}{
		{"public", false, 0},
		{"private", true, discord.MessageFlagEphemeral},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := mustTestClient(t)
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				var response struct {
					Type discord.InteractionResponseType `json:"type"`
					Data struct {
						Flags discord.MessageFlags `json:"flags"`
					} `json:"data"`
				}
				if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
					t.Fatal(err)
				}
				if response.Type != discord.InteractionResponseTypeDeferredCreateMessage || response.Data.Flags != test.flags {
					t.Fatalf("deferred callback = %+v, want flags %d", response, test.flags)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})})
			interaction := testCommandInteraction(t, client, `{"id":"17","application_id":"1","token":"token","type":2,"channel":{"id":"5","type":0},"data":{"name":"ping","type":1}}`)
			if err := deferResponse(client, interaction, test.ephemeral); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNoPingMessageDisablesMentions(t *testing.T) {
	msg := noPingMessage("<@123>")
	if msg.AllowedMentions == nil || len(msg.AllowedMentions.Parse) != 0 || len(msg.AllowedMentions.Users) != 0 {
		t.Fatalf("noPingMessage() allowed mentions = %#v", msg.AllowedMentions)
	}
}

func TestSuppressDiscordEmbedsWrapsBareURLs(t *testing.T) {
	input := "compare: https://example.com/release"
	want := "compare: <https://example.com/release>"

	if got := suppressDiscordEmbeds(input); got != want {
		t.Fatalf("suppressDiscordEmbeds() = %q, want %q", got, want)
	}
}

func TestSuppressDiscordEmbedsKeepsWrappedURLs(t *testing.T) {
	input := "download: <https://example.com/release>"
	want := input

	if got := suppressDiscordEmbeds(input); got != want {
		t.Fatalf("suppressDiscordEmbeds() = %q, want %q", got, want)
	}
}

func TestSuppressDiscordEmbedsSkipsCodeFences(t *testing.T) {
	input := "```\nhttps://example.com/in-code\n```\noutside https://example.com/outside"
	want := "```\nhttps://example.com/in-code\n```\noutside <https://example.com/outside>"

	if got := suppressDiscordEmbeds(input); got != want {
		t.Fatalf("suppressDiscordEmbeds() = %q, want %q", got, want)
	}
}
