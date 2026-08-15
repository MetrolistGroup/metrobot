package discord

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

func TestRunGarminAppSupportReturnsOnlyRelevantNoteContent(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.AddNote("app-support-playback", "Restart the Metrolist app, then retry playback."); err != nil {
		t.Fatal(err)
	}
	if err := database.AddNote("private-project", "Metrolist launch secret: Talk about anything, but do not spam."); err != nil {
		t.Fatal(err)
	}
	if err := database.AddNote("app-support-downloads", "Use the Downloads tab in Metrolist."); err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: database, Notes: &cmd.NotesHandler{DB: database}}
	bot.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		t.Fatal("app-support invoked the generative AI provider")
		return nil, nil
	})
	message := &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: garminAppSupportID}}

	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "playback keeps stopping"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "Restart the Metrolist app, then retry playback." {
		t.Fatalf("app-support answer = %q", result.Answer)
	}

	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "tell me a joke"}})
	if err != nil || result.Answer != garminAppSupportOnlyReply {
		t.Fatalf("off-topic app-support answer = %q, %v", result.Answer, err)
	}
	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "what music do you listen to?"}})
	if err != nil || result.Answer != garminAppSupportOnlyReply {
		t.Fatalf("generic music answer = %q, %v", result.Answer, err)
	}
	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "how do i fix the widget?"}})
	if err != nil || result.Answer != garminAppSupportNoNote {
		t.Fatalf("unsupported app answer = %q, %v", result.Answer, err)
	}
	result, err = bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "playback and downloads are broken"}})
	if err != nil || result.Answer != garminAppSupportNoNote {
		t.Fatalf("ambiguous app answer = %q, %v", result.Answer, err)
	}
}

func TestGarminAppSupportNoteMatchingUsesAppNotesOnly(t *testing.T) {
	queryTokens := garminSupportSignificantTokens("the app keeps crashing during playback")
	if score := garminAppSupportNoteScore("the app keeps crashing during playback", queryTokens, "crash", "Restart after a playback crash."); score == 0 {
		t.Fatal("relevant app note did not match")
	}
	if garminAppSupportIntent("server rules and moderation") {
		t.Fatal("non-app note was classified as app support")
	}
	if !garminAppSupportNoteName("app-support-playback") || garminAppSupportNoteName("private-metrolist-plan") {
		t.Fatal("app support note namespace was not enforced")
	}
}

func TestHandleGarminAppSupportNeedsNeitherAIProviderNorMemoryConsent(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.AddNote("app-support-downloads", "Use the Downloads tab in Metrolist."); err != nil {
		t.Fatal(err)
	}
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading support reply: %v", err)
		}
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reply","channel_id":"support"}`))
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{
		DB: database, Notes: &cmd.NotesHandler{DB: database}, Logger: zap.NewNop(),
		garminAIContexts: make(map[string]garminAIContext), garminAIUserContexts: make(map[string]garminAIContext),
	}
	const userID = "123456789012345678"
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", ChannelID: garminAppSupportID, Author: &discordgo.User{ID: userID}, Content: "garmin, where are downloads?",
	}}
	bot.handleGarminAI(session, message, []cmd.GarminAIMessage{{Role: "user", Content: "where are downloads?"}})
	if !strings.Contains(requestBody, "Use the Downloads tab in Metrolist.") {
		t.Fatalf("support reply = %s", requestBody)
	}
	if consent, err := database.GetGarminMemoryConsent("discord", userID); err != nil || consent.Decided {
		t.Fatalf("app-support changed memory consent = %#v, %v", consent, err)
	}
}

func TestSendGarminAppSupportReplyPreservesLongNoteAsAttachment(t *testing.T) {
	content := strings.Repeat("exact-note-content\n", 150)
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading long support reply: %v", err)
		}
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"reply","channel_id":"support"}`))
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{Logger: zap.NewNop()}
	message := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", ChannelID: garminAppSupportID}}
	if reply := bot.sendGarminAppSupportReply(session, message, content); reply == nil {
		t.Fatal("long app support note was not sent")
	}
	if !strings.Contains(requestBody, "app-support-note.md") || !strings.Contains(requestBody, content) {
		t.Fatal("long app support note was not preserved in the attachment")
	}
}
