package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

func TestTruncateGarminAIResponse(t *testing.T) {
	input := strings.Repeat("a", garminAIMaxContent+1)
	got := truncateGarminAIResponse(input)
	if len(got) > garminAIMaxContent {
		t.Fatalf("response length = %d, max %d", len(got), garminAIMaxContent)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated response = %q, want ellipsis", got)
	}
}

func TestTruncateGarminAIResponsePreservesUTF8(t *testing.T) {
	input := strings.Repeat("é", garminAIMaxContent)
	got := truncateGarminAIResponse(input)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated response is invalid UTF-8: %q", got)
	}
}

func TestGarminAIContinuationIncludesBoundedContext(t *testing.T) {
	bot := &Bot{
		garminAI:         &fakeGarminAI{},
		garminAIContexts: make(map[string]garminAIContext),
	}
	history := []cmd.GarminAIMessage{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "one answer"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "two answer"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "three answer"},
	}
	bot.rememberGarminAIContext("bot-message", history)

	messages, ok := bot.garminAIContinuation(&discordgo.MessageCreate{Message: &discordgo.Message{
		MessageReference: &discordgo.MessageReference{MessageID: "bot-message"},
	}}, "  follow up  ")
	if !ok {
		t.Fatal("garminAIContinuation() did not recognize tracked reply")
	}
	want := []cmd.GarminAIMessage{
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "two answer"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "three answer"},
		{Role: "user", Content: "follow up"},
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("messages = %#v, want %#v", messages, want)
	}
}

func TestGarminAIContinuationRejectsUntrackedAndExpiredReplies(t *testing.T) {
	bot := &Bot{
		garminAI: &fakeGarminAI{},
		garminAIContexts: map[string]garminAIContext{
			"expired": {expiresAt: time.Now().Add(-time.Minute)},
		},
	}
	for _, messageID := range []string{"other-bot-message", "expired"} {
		messages, ok := bot.garminAIContinuation(&discordgo.MessageCreate{Message: &discordgo.Message{
			MessageReference: &discordgo.MessageReference{MessageID: messageID},
		}}, "follow up")
		if ok || messages != nil {
			t.Fatalf("garminAIContinuation(%q) = (%#v, %v), want rejected", messageID, messages, ok)
		}
	}
}

func TestGarminAIUserMessageIncludesImageAttachments(t *testing.T) {
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{
			{Filename: "photo.png", ContentType: "image/png", URL: "https://cdn.discordapp.com/attachments/photo.png"},
			{Filename: "notes.txt", ContentType: "text/plain", URL: "https://cdn.discordapp.com/attachments/notes.txt"},
			{Filename: "fallback.webp", URL: "https://media.discordapp.net/attachments/fallback.webp"},
		},
	}}
	got := garminAIUserMessage(message, "  what is this?  ")
	want := cmd.GarminAIMessage{
		Role:    "user",
		Content: "what is this?",
		Images: []string{
			"https://cdn.discordapp.com/attachments/photo.png",
			"https://media.discordapp.net/attachments/fallback.webp",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("garminAIUserMessage() = %#v, want %#v", got, want)
	}
}

func TestGarminAIUserMessageDefaultsImageOnlyPrompt(t *testing.T) {
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{{
			Filename: "photo.jpg", ContentType: "image/jpeg", URL: "https://cdn.discordapp.com/photo.jpg",
		}},
	}}
	got := garminAIUserMessage(message, "")
	if got.Content != "what is in this image?" || len(got.Images) != 1 {
		t.Fatalf("image-only message = %#v", got)
	}
}

func TestSendGarminReplyRetriesWithoutReference(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if requests == 1 {
			if _, ok := payload["message_reference"]; !ok {
				t.Error("first request omitted message reference")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Unknown message","code":10008}`))
			return
		}
		if _, ok := payload["message_reference"]; ok {
			t.Error("fallback request included message reference")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"bot-reply","channel_id":"channel","content":"answer"}`))
	}))
	defer server.Close()

	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{Logger: zap.NewNop()}
	reply := bot.sendGarminReply(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "original", ChannelID: "channel",
	}}, "answer")
	if reply == nil || reply.ID != "bot-reply" || requests != 2 {
		t.Fatalf("sendGarminReply() = %#v after %d requests", reply, requests)
	}
}

type rewriteDiscordTransport struct {
	base   http.RoundTripper
	target string
}

func (r rewriteDiscordTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme = "http"
	request.URL.Host = strings.TrimPrefix(r.target, "http://")
	return r.base.RoundTrip(request)
}

type fakeGarminAI struct{}

func (*fakeGarminAI) Complete(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
	return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "ok"}}, nil
}
