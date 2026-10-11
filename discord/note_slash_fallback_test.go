package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/internal/notefmt"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestRichSlashRejectionFallsBackToReadableNote(t *testing.T) {
	for _, kind := range []string{"components-v2", "faq"} {
		for _, long := range []bool{false, true} {
			for _, ephemeral := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/long=%v/ephemeral=%v", kind, long, ephemeral), func(t *testing.T) {
					text := "Help @everyone <@123> <@&456>."
					if long {
						text += strings.Repeat(" full 界𝄞", 250)
					}
					encoded, err := json.Marshal(text)
					if err != nil {
						t.Fatal(err)
					}
					body := `{"components":[{"type":"text","content":` + string(encoded) + `},{"type":"buttons","buttons":[{"label":"Open","url":"https://example.com"}]}]}`
					if kind == "faq" {
						body = "# Help\n## Question\n" + text + "\n## Links\nhttps://example.com"
					}
					doc, err := notefmt.Parse("Before\n" + notefmt.Fence(kind, body) + "\nAfter")
					if err != nil {
						t.Fatal(err)
					}
					if err := doc.Validate(); err != nil {
						t.Fatal(err)
					}
					requests := make(chan deliveredNote, 2)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						got := readDeliveredNote(t, r)
						requests <- got
						if got.Message.Flags.Has(discord.MessageFlagIsComponentsV2) {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"message":"Invalid Form Body","code":50035}`))
							return
						}
						w.WriteHeader(http.StatusNoContent)
					}))
					defer server.Close()
					client := mustTestClient(t)
					client.Rest = testREST(server.Client())
					client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
					interaction := testCommandInteraction(t, client, `{"id":"17","application_id":"1","token":"token","type":2,"channel":{"id":"5","type":0},"data":{"name":"note","type":1}}`)
					note := doc.Render()
					b := &Bot{Logger: zap.NewNop()}
					b.respondNote(client, interaction, "authored", note, ephemeral)
					if len(requests) != 2 {
						t.Fatalf("requests = %d; wanted rejected rich response then readable fallback", len(requests))
					}
					first, fallback := <-requests, <-requests
					if !first.Message.Flags.Has(discord.MessageFlagIsComponentsV2) || first.Path != fallback.Path || first.Type != discord.InteractionResponseTypeCreateMessage || fallback.Type != discord.InteractionResponseTypeCreateMessage {
						t.Fatal("fallback did not retry the same interaction with a readable response")
					}
					flags := discord.MessageFlags(0)
					if ephemeral {
						flags = discord.MessageFlagEphemeral
					}
					if first.Message.Flags.Has(discord.MessageFlagEphemeral) != ephemeral || fallback.Message.Flags != flags || len(fallback.Message.Components) != 0 {
						t.Fatal("fallback changed visibility or retained rich flags/components")
					}
					mentions := fallback.Message.AllowedMentions
					if mentions == nil || len(mentions.Parse) != 0 || len(mentions.Users) != 0 || len(mentions.Roles) != 0 {
						t.Fatal("fallback broadened allowed mentions")
					}
					if long {
						if fallback.Filename != "note.md" || fallback.FileContent != note.Text || fallback.Message.Content != "" {
							t.Fatal("fallback attachment lost full readable content")
						}
					} else if fallback.Filename != "" || fallback.Message.Content != note.Text {
						t.Fatal("fallback lost readable text")
					}
				})
			}
		}
	}
}
