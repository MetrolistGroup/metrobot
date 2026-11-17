package discord

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
)

func TestSupportAgentBoundsLongConversationsWithoutExtraModelCall(t *testing.T) {
	b, client, _ := newSupportDecisionTestBot(t)
	m := supportTestMessage(20)
	var messages []cmd.GarminAIMessage
	for i := range 43 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages = append(messages, cmd.GarminAIMessage{Role: role, Content: fmt.Sprintf("troubleshooting step %d", i)})
	}
	calls := 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if len(request.Messages) > garminAIConversationWindow+1 || request.Messages[0].Content != garminAIConversationOmissionNote || request.Messages[1].Role != "user" || request.Messages[len(request.Messages)-1].Content != "troubleshooting step 42" {
			t.Fatalf("wrong support window: %#v", request.Messages)
		}
		return garminHistoryTestCall("do_not_respond", `{}`), nil
	})
	if !b.runSupportDecisionAgent(client, m, messages, "") || calls != 1 {
		t.Fatalf("handoff calls=%d", calls)
	}
}

func TestSupportDecisionActionDefersUncertainMatches(t *testing.T) {
	for _, tc := range []struct {
		name                string
		answer, top, margin float64
		route, note         string
		agent               bool
		want                string
	}{
		{"clear note", .99, .98, .96, "n0", "nightly", true, "note"},
		{"uncertain note", .95, .60, .20, "n0", "nightly", true, "agent"},
		{"close alternatives", .95, .85, .10, "n0", "nightly", true, "agent"},
		{"unmatched request", .90, .60, .20, "agent", "", true, "agent"},
		{"low request probability", .79, .99, .98, "n0", "nightly", true, "none"},
		{"confident request routed to none", .99, .99, .98, "none", "", true, "agent"},
		{"uncertain request routed to none", .79, .99, .98, "none", "", true, "none"},
		{"no agent available", .99, .60, .20, "agent", "", false, "none"},
		{"confident note still needs agent", .99, .99, .98, "n0", "nightly", false, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := supportDecisionAction(tc.answer, tc.top, tc.margin, tc.route, tc.note, tc.agent)
			if got != tc.want {
				t.Fatalf("action=%s want=%s", got, tc.want)
			}
		})
	}
}

// A reply to the bot is explicit, so the router cannot drop it.
func TestAppSupportReplyToBotSkipsRouter(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	client.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: 900}})
	var routed atomic.Bool
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		if _, routing := request.Questions["route"]; routing {
			routed.Store(true)
			return supportTestDecision(request, "none", 0, 0), nil
		}
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "which Metrolist version are you on?"}}, nil
	})
	m := supportTestMessage(20)
	m.Content = "that didn't work"
	m.ReferencedMessage = &discord.Message{ID: snowflakeID("19"), ChannelID: m.ChannelID, Author: discord.User{ID: 900}}
	b.onMessageCreate(client, m)
	for deadline := time.Now().Add(2 * time.Second); sent.count() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	sent.mu.Lock()
	defer sent.mu.Unlock()
	if routed.Load() || len(sent.bodies) != 1 || !strings.Contains(sent.bodies[0], "which Metrolist version") {
		t.Fatalf("routed=%v deliveries=%d", routed.Load(), len(sent.bodies))
	}
}
