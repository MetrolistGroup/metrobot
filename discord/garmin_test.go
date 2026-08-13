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
		garminAI:             &fakeGarminAI{},
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	history := []cmd.GarminAIMessage{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "one answer"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "two answer"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "three answer"},
	}
	bot.rememberGarminAIContext("bot-message", "user", history)

	messages, ok := bot.garminAIContinuation(&discordgo.MessageCreate{Message: &discordgo.Message{
		MessageReference: &discordgo.MessageReference{MessageID: "bot-message"},
		Author:           &discordgo.User{ID: "user"},
	}}, "  follow up  ")
	if !ok {
		t.Fatal("garminAIContinuation() did not recognize tracked reply")
	}
	want := []cmd.GarminAIMessage{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "one answer"},
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
			Author:           &discordgo.User{ID: "user"},
		}}, "follow up")
		if ok || messages != nil {
			t.Fatalf("garminAIContinuation(%q) = (%#v, %v), want rejected", messageID, messages, ok)
		}
	}
}

func TestGarminAIContinuationDoesNotWakeOnHumanReplyWithUserHistory(t *testing.T) {
	bot := &Bot{
		garminAI:             &fakeGarminAI{},
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	bot.rememberGarminAIContext("tracked-bot-reply", "nyx", []cmd.GarminAIMessage{
		{Role: "user", Content: "garmin, hello"},
		{Role: "assistant", Content: "hey nyx"},
	})
	messages, ok := bot.garminAIContinuation(&discordgo.MessageCreate{Message: &discordgo.Message{
		MessageReference:  &discordgo.MessageReference{MessageID: "human-message"},
		ReferencedMessage: &discordgo.Message{ID: "human-message", Author: &discordgo.User{ID: "human"}},
		Author:            &discordgo.User{ID: "nyx"},
	}}, "me and lamp")
	if ok || messages != nil {
		t.Fatalf("human reply woke Garmin with messages %#v", messages)
	}
}

func TestGarminAITriggeredConversationUsesPerUserHistory(t *testing.T) {
	bot := &Bot{
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	history := []cmd.GarminAIMessage{
		{Role: "user", Content: "always add a heart"},
		{Role: "assistant", Content: "got it ❤️"},
	}
	bot.rememberGarminAIContext("old-reply", "john", history)
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		Author: &discordgo.User{ID: "john"},
	}}
	got := bot.garminAITriggeredConversation(message, "you forgot")
	want := append(history, cmd.GarminAIMessage{Role: "user", Content: "you forgot"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("triggered conversation = %#v, want %#v", got, want)
	}
}

func TestExpandGarminAIEmojis(t *testing.T) {
	got := expandGarminAIEmojis(nil, "", "nice :thumb: :trolleyz: :not_allowed:")
	want := "nice <:thumb:1481187881946058922> <a:trolleyz:1481188261274587217> :not_allowed:"
	if got != want {
		t.Fatalf("expandGarminAIEmojis() = %q, want %q", got, want)
	}
}

func TestGarminAIEmojiCatalogContainsAllCurrentGuildEmojis(t *testing.T) {
	if len(garminAIEmojis) != 46 {
		t.Fatalf("emoji catalog has %d entries, want 46", len(garminAIEmojis))
	}
	for name, emoji := range garminAIEmojis {
		if emoji.Name != name || emoji.ID == "" || !emoji.Available {
			t.Errorf("invalid emoji %q: %#v", name, emoji)
		}
	}
	for _, name := range []string{"soggy", "thumb", "painfade", "cozystars"} {
		if _, ok := garminAIEmojis[name]; !ok {
			t.Errorf("emoji catalog missing %q", name)
		}
	}
}

func TestGarminAIEmojiOnlyBecomesReactionCandidate(t *testing.T) {
	for _, content := range []string{":soggy:", "  :thumb:  ", "<a:trolleyz:1481188261274587217>"} {
		if emoji := garminAIEmojiOnly(content); emoji == nil {
			t.Errorf("garminAIEmojiOnly(%q) = nil", content)
		}
	}
	for _, content := range []string{"ouch :soggy:", ":not_allowed:", "hello"} {
		if emoji := garminAIEmojiOnly(content); emoji != nil {
			t.Errorf("garminAIEmojiOnly(%q) = %#v, want nil", content, emoji)
		}
	}
}

func TestHandleGarminAIDoNotRespond(t *testing.T) {
	handled, err := handleGarminAIMessageAction(nil, nil, cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{
		Name:      "do_not_respond",
		Arguments: `{}`,
	}})
	if err != nil || !handled {
		t.Fatalf("do_not_respond = (%v, %v), want handled", handled, err)
	}
}

func TestGarminDirectSlurDetection(t *testing.T) {
	for _, prompt := range []string{
		"you are a nigger",
		"f.a.g.g.o.t",
		"stupid r3tard",
	} {
		if !garminDirectSlur(prompt) {
			t.Errorf("garminDirectSlur(%q) = false, want true", prompt)
		}
	}
	for _, prompt := range []string{
		"what does the n-word mean?",
		"someone called me a faggot",
		"why is nigger a slur?",
		"fuck you",
		"hello",
	} {
		if garminDirectSlur(prompt) {
			t.Errorf("garminDirectSlur(%q) = true, want false", prompt)
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
