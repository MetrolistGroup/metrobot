package discord

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestSupportContextRecordsDeliveredRandomizedNote(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	notes := &cmd.NotesHandler{DB: database}
	if err := notes.AddNote("tips", "Helpful tips", "Intro\n```random\n[\"first\",\"second\",\"third\"]\n```"); err != nil {
		t.Fatal(err)
	}
	requests := make(chan deliveredNote, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- readDeliveredNote(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"38","channel_id":"5"}`))
	}))
	defer server.Close()
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	b := &Bot{DB: database, Notes: notes, Logger: zap.NewNop()}
	original := []cmd.GarminAIMessage{{Role: "user", Content: "tips?"}, {Role: "assistant", Content: "earlier rendition"}}
	m := &discord.Message{ID: 23, ChannelID: 5, Author: discord.User{ID: 42}}
	if b.sendGarminAppSupportReplyAndRememberIfVisible(client, m, "earlier rendition", original, "tips") == nil {
		t.Fatal("send failed")
	}
	sent := <-requests
	context := b.garminAIContexts["38"].messages
	if len(context) == 0 || context[len(context)-1].Content != sent.Message.Content {
		t.Fatalf("stored answer differs from delivered note: %+v", context)
	}
	if original[1].Content != "earlier rendition" {
		t.Fatal("mutated caller conversation")
	}
}
