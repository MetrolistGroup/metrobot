package discord

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestGarminConversationWindowKeepsRecentCompleteExchanges(t *testing.T) {
	for _, test := range []struct{ count, start int }{{0, 0}, {1, 0}, {29, 0}, {30, 0}, {31, 2}, {32, 2}, {33, 4}, {100, 70}} {
		t.Run(fmt.Sprint(test.count), func(t *testing.T) {
			var messages []cmd.GarminAIMessage
			for i := range test.count {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				messages = append(messages, cmd.GarminAIMessage{Role: role, Name: "discord_56", Content: fmt.Sprintf("message %d", i), Timestamp: time.Unix(int64(i), 0).UTC(), Images: []string{"original image"}})
			}
			original := copyGarminAIMessages(messages)
			got := boundGarminAIConversation(messages)
			start := test.start
			retained := got
			if start > 0 {
				if got[0].Name != garminAIConversationSummaryName || got[0].Content != garminAIConversationOmissionNote {
					t.Fatalf("missing omission note: %#v", got)
				}
				retained = got[1:]
			}
			if len(retained) > garminAIConversationWindow || len(retained) > 0 && retained[0].Role != "user" || !reflect.DeepEqual(retained, messages[start:]) {
				t.Fatalf("wrong window: %#v", got)
			}
			if !reflect.DeepEqual(boundGarminAIConversation(got), got) {
				t.Fatal("bounding duplicated the note or dropped more messages")
			}
			if !reflect.DeepEqual(messages, original) {
				t.Fatal("bounding mutated the input")
			}
			if len(retained) > 0 {
				retained[0].Images[0] = "changed image"
				if messages[start].Images[0] != "original image" {
					t.Fatal("window aliases mutable input")
				}
			}
		})
	}
}

func TestGarminConversationWindowDropsLegacySummariesAndOrphanReplies(t *testing.T) {
	for _, summary := range []string{"", "unstructured summary", `{"summary":"older model output"}`, garminAIConversationOmissionNote} {
		messages := []cmd.GarminAIMessage{
			{Role: "assistant", Name: garminAIConversationSummaryName, Content: summary},
			{Role: "assistant", Content: "orphan reply"},
			{Role: "user", Name: "discord_56", Content: "current request"},
		}
		want := []cmd.GarminAIMessage{
			{Role: "assistant", Name: garminAIConversationSummaryName, Content: garminAIConversationOmissionNote},
			messages[2],
		}
		if got := boundGarminAIConversation(messages); !reflect.DeepEqual(got, want) {
			t.Fatalf("legacy summary %q = %#v, want %#v", summary, got, want)
		}
	}
}

func TestGarminLongConversationUsesOneModelCallPerTurn(t *testing.T) {
	calls, sends := 0, 0
	client := lifecycleClient(t, func() (string, error) {
		sends++
		return fmt.Sprintf(`{"id":"%d","channel_id":"5"}`, 300+sends), nil
	})
	b := lifecycleBot(garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if len(request.Messages) > garminAIConversationWindow+1 || request.Messages[0].Content != garminAIConversationOmissionNote || request.Messages[1].Role != "user" {
			t.Fatalf("answer model received an unbounded conversation: %#v", request.Messages)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "answer"}}, nil
	}))
	var messages []cmd.GarminAIMessage
	for i := range 43 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages = append(messages, cmd.GarminAIMessage{Role: role, Content: fmt.Sprint(i)})
	}
	for turn := range 3 {
		m := lifecycleMessage(fmt.Sprint(100 + turn))
		m.Content = "garmin, continue"
		if turn > 0 {
			messages = b.garminAITriggeredConversation(m, "continue")
		}
		b.garminAILastUsed[m.Author.ID.String()] = time.Time{}
		b.handleGarminAI(client, m, messages)
		if calls != turn+1 || sends != turn+1 {
			t.Fatalf("turn %d made %d model calls and sent %d replies", turn, calls, sends)
		}
	}
}

func TestGarminWindowPreservesContextTTLAndImageRetention(t *testing.T) {
	b := &Bot{}
	m := lifecycleMessage("100")
	now := time.Now()
	var messages []cmd.GarminAIMessage
	for range 40 {
		messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: "question", Images: []string{"image"}})
	}
	b.storeGarminAIContextLocked("150", m, messages, now)
	retained := b.garminAIContexts["150"]
	if !retained.expiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("context TTL changed: %v", retained.expiresAt.Sub(now))
	}
	images := 0
	for _, message := range retained.messages {
		images += len(message.Images)
	}
	if images != 10 {
		t.Fatalf("retained images for %d messages, want 10", images)
	}
}
