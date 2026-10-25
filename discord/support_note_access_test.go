package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

func TestSupportFallbackDoesNotRepeatAmbiguousSend(t *testing.T) {
	for _, note := range []string{"", "nightly"} {
		t.Run("note="+note, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				if note != "" && requests == 1 {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"message":"Invalid components","code":50035}`))
					return
				}
				// Discord may have posted the message even though its response was truncated.
				_, _ = w.Write([]byte(`{"id":`))
			}))
			defer server.Close()
			client := mustTestClient(t)
			client.Rest = testREST(server.Client())
			client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
			b := &Bot{Logger: zap.NewNop()}
			reply, _ := b.sendGarminAppSupportReply(client, supportTestMessage(20), "Relevant excerpt.", note)
			want := 1
			if note != "" {
				want++
			}
			if reply != nil || requests != want {
				t.Fatalf("ambiguous send retried: requests=%d want=%d reply=%v", requests, want, reply)
			}
		})
	}
}

// Exercise the interaction API's disclosure boundary, not the rendered UI.
func TestSupportNoteAccessIsPrivateAndUsesCurrentContent(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	name := strings.Repeat("界𝄞", 80)
	if err := b.Notes.AddNote(name, "Reference", "Original reference"); err != nil {
		t.Fatal(err)
	}
	id := noteMoreCustomID(name)
	if len(id) > 100 {
		t.Fatal("unbounded interaction identifier")
	}
	var interaction discord.ComponentInteraction
	payload := `{"id":"17","application_id":"1","token":"token","type":3,"channel":{"id":"5","type":0},"message":{"id":"38","flags":32768},"data":{"custom_id":"` + id + `","component_type":2}}`
	if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
		t.Fatal(err)
	}
	if err := b.Notes.EditNote(name, "Reference", "Current full reference"); err != nil {
		t.Fatal(err)
	}
	for _, deleted := range []bool{false, true} {
		if deleted {
			if err := b.Notes.DeleteNote(name); err != nil {
				t.Fatal(err)
			}
		}
		before := sent.count()
		if !b.handleNoteComponent(client, &events.ComponentInteractionCreate{ComponentInteraction: interaction}) || sent.count() != before+1 {
			t.Fatal("note access was not acknowledged exactly once")
		}
		var response struct {
			Type discord.InteractionResponseType `json:"type"`
			Data discord.MessageCreate           `json:"data"`
		}
		if err := json.Unmarshal([]byte(sent.bodies[before]), &response); err != nil {
			t.Fatal(err)
		}
		if response.Type != discord.InteractionResponseTypeCreateMessage || !response.Data.Flags.Has(discord.MessageFlagEphemeral) {
			t.Fatal("note access replaced the public answer or disclosed the note publicly")
		}
		if !deleted && (response.Data.AllowedMentions == nil || len(response.Data.AllowedMentions.Parse) != 0 || len(response.Data.AllowedMentions.Users) != 0 || len(response.Data.AllowedMentions.Roles) != 0) {
			t.Fatal("private note access allowed unsolicited mentions")
		}
		if !deleted && response.Data.Content != "Current full reference" {
			t.Fatal("explicit access did not read the current note")
		}
		if deleted && strings.Contains(response.Data.Content, "reference") {
			t.Fatal("deleted note contents remained accessible")
		}
	}
}
