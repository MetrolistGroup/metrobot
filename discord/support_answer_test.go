package discord

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
)

func TestSupportAnswerGroundingFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name                    string
		probability             float64
		missing, apiError, want bool
	}{
		{name: "supported", probability: 0.99, want: true},
		{name: "threshold", probability: 0.95, want: true},
		{name: "uncertain", probability: 0.94},
		{name: "unsupported", probability: 0.01},
		{name: "missing", missing: true},
		{name: "API failure", probability: 1, apiError: true},
		{name: "NaN", probability: math.NaN()},
		{name: "infinite", probability: math.Inf(1)},
		{name: "out of range", probability: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			messages := []cmd.GarminAIMessage{{Role: "tool", Content: "OLD_TOOL_RESULT"}, {Role: "user", Content: "search is broken"}}
			conversation := append(copyGarminAIMessages(messages),
				cmd.GarminAIMessage{Role: "assistant", ToolCalls: []cmd.GarminAIToolCall{{ID: "note-call", Function: cmd.GarminAIFunctionCall{Name: "get_note", Arguments: `{"name":"nightly"}`}}}},
				cmd.GarminAIMessage{Role: "tool", ToolCallID: "note-call", Content: `CURRENT_TOOL_RESULT {"latest_release":"stale note data"}`},
			)
			b := &Bot{garminDecisions: supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				state, err := json.Marshal(request.State)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(state), "OLD_TOOL_RESULT") || !strings.Contains(string(state), "CURRENT_TOOL_RESULT") || !strings.Contains(string(state), "proposed_answer") {
					t.Fatal("grounding request mixed old tool provenance or lost current evidence")
				}
				if !strings.Contains(string(state), `"tool":"get_note"`) || strings.Contains(string(state), `"tool":"get_metrolist_status"`) || !strings.Contains(string(state), `"arguments":`) {
					t.Fatal("saved note was not attributed to its actual tool and arguments")
				}
				if len(request.Questions) != 1 || request.Questions["grounded"].Type != "noul" || len(request.Questions["grounded"].Criteria) != 2 {
					t.Fatal("wrong grounding question")
				}
				if test.apiError {
					return nil, errors.New("unavailable")
				}
				if test.missing {
					return &cmd.DecisionResponse{}, nil
				}
				return &cmd.DecisionResponse{Answers: map[string]cmd.DecisionAnswer{"grounded": {Type: "noul", Noul: &test.probability}}}, nil
			})}
			if got, _ := b.supportAnswerGrounded(context.Background(), supportTestMessage(101), messages, conversation, "try a clean install"); got != test.want {
				t.Fatalf("grounded = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSupportEvidenceTracksCurrentAttachmentAvailability(t *testing.T) {
	for _, test := range []struct {
		name        string
		attachments []discord.Attachment
		images      []string
		unread      bool
	}{
		{name: "automatic image omitted", attachments: []discord.Attachment{{URL: "https://example.org/current.png"}}, unread: true},
		{name: "current image supplied", attachments: []discord.Attachment{{URL: "https://example.org/current.png"}}, images: []string{"https://example.org/current.png"}},
		{name: "log plus replied-to image", attachments: []discord.Attachment{{Filename: "message.txt", URL: "https://example.org/log.txt"}}, images: []string{"https://example.org/parent.png"}, unread: true},
		{name: "image and unread log", attachments: []discord.Attachment{{URL: "https://example.org/current.png"}, {URL: "https://example.org/log.txt"}}, images: []string{"https://example.org/current.png"}, unread: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := &Bot{garminDecisions: supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				if got := request.State.(map[string]any)["unread_attachments"]; got != test.unread {
					t.Fatalf("unread = %v, want %v", got, test.unread)
				}
				return supportTestDecision(request, "agent", .99, .99), nil
			})}
			m := supportTestMessage(101)
			m.Attachments = test.attachments
			messages := []cmd.GarminAIMessage{{Role: "user", Content: "see attached", Images: test.images}}
			if _, err := b.supportAnswerGrounded(context.Background(), m, messages, messages, "Please paste the error text."); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSupportAgentSuppressesUngroundedDraft(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		probability := 0.01
		if accepted {
			probability = 0.99
		}
		b := &Bot{
			garminAI: garminAITestFunc(func(_ context.Context, _ cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Which app version are you using?"}}, nil
			}),
			garminDecisions: supportDecisionTestFunc(func(_ context.Context, _ cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				return &cmd.DecisionResponse{Answers: map[string]cmd.DecisionAnswer{"grounded": {Type: "noul", Noul: &probability}}}, nil
			}),
		}
		message := supportTestMessage(101)
		message.Content = "search isn't working"
		result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: message.Content}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Silent == accepted || (result.Answer != "") != accepted {
			t.Fatalf("accepted=%v: answer=%q silent=%v", accepted, result.Answer, result.Silent)
		}
	}
}
