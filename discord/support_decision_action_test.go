package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestSupportAgentCompactsLongConversations(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("summary_failure_%v", fail), func(t *testing.T) {
			b, client, _ := newSupportDecisionTestBot(t)
			m := supportTestMessage(20)
			var messages []cmd.GarminAIMessage
			for i := 0; i < 43; i++ {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				messages = append(messages, cmd.GarminAIMessage{Role: role, Content: fmt.Sprintf("troubleshooting step %d", i)})
			}
			calls := 0
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if calls == 1 {
					if request.SystemPrompt != garminAIConversationSummaryPrompt {
						t.Fatal("handoff skipped compaction")
					}
					if fail {
						return nil, errors.New("synthetic summary failure")
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: `{"summary":"Early troubleshooting steps did not fix playback.","shared_memory":[]}`}}, nil
				}
				if !fail && (len(request.Messages) == 0 || request.Messages[0].Name != garminAIConversationSummaryName || !strings.Contains(request.Messages[0].Content, "Early troubleshooting")) {
					t.Fatal("agent lost summarized troubleshooting")
				}
				if fail && len(request.Messages) > 2*garminAICompactionMessages {
					t.Fatal("failed compaction did not bound history")
				}
				return garminHistoryTestCall("do_not_respond", `{}`), nil
			})
			if !b.runSupportDecisionAgent(client, m, messages) || calls != 2 {
				t.Fatalf("handoff calls=%d", calls)
			}
		})
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := supportDecisionAction(tc.answer, tc.top, tc.margin, tc.route, tc.note, tc.agent)
			if got != tc.want {
				t.Fatalf("action=%s want=%s", got, tc.want)
			}
		})
	}
}
