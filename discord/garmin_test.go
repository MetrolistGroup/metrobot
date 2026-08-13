package discord

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/bwmarrin/discordgo"
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

type fakeGarminAI struct{}

func (*fakeGarminAI) Ask(context.Context, []cmd.GarminAIMessage) (string, error) {
	return "", nil
}
