package discord

import (
	"context"
	"fmt"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
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
		{"explicit silence", .99, .99, .98, "none", "", true, "none"},
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
