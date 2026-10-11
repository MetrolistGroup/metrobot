package discord

import (
	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/notefmt"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDonorWelcomeRichRejectionKeepsTargetOnlyFallback(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	notes := &cmd.NotesHandler{DB: database}
	if err := notes.AddNote(donorWelcomeNote, "Welcome", notefmt.Fence("components-v2", `{"components":[{"type":"text","content":"Welcome @everyone <@42>"}]}`)); err != nil {
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"38","channel_id":"5"}`))
	}))
	defer server.Close()
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	(&Bot{Notes: notes, Logger: zap.NewNop()}).sendDonorWelcome(client, "56")
	if len(requests) != 2 {
		t.Fatalf("sent %d requests", len(requests))
	}
	<-requests
	got := <-requests
	if got.Message.Content != "<@56>\nWelcome @everyone <@42>" || !strings.Contains(got.Path, donorWelcomeChannelID) {
		t.Fatal("fallback lost content or channel")
	}
	mentions := got.Message.AllowedMentions
	if mentions == nil || len(mentions.Users) != 1 || mentions.Users[0] != 56 || len(mentions.Parse) != 0 || len(mentions.Roles) != 0 {
		t.Fatal("fallback broadened mentions")
	}
}
