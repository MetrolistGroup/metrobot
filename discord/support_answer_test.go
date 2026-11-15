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

func TestSupportAnswerBrevity(t *testing.T) {
	for _, test := range []struct {
		name, prompt, answer string
		channel              string
		want                 bool
	}{
		{name: "short support", prompt: "fullscreen artwork is broken", answer: "which Metrolist version are you using?", want: true},
		{name: "word limit", prompt: "fullscreen artwork is broken", answer: strings.Repeat("word ", 60), want: true},
		{name: "verbose support", prompt: "fullscreen artwork is broken", answer: strings.Repeat("word ", 61)},
		{name: "requested detail", prompt: "explain the setup in detail", answer: strings.Repeat("word ", 61), want: true},
		{name: "other channel", prompt: "fullscreen artwork is broken", answer: strings.Repeat("word ", 61), channel: garminChatThreadID, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := &Bot{}
			message := supportTestMessage(20)
			message.Content = test.prompt
			if test.channel != "" {
				message.ChannelID = snowflakeID(test.channel)
			}
			messages := []cmd.GarminAIMessage{{Role: "user", Content: test.prompt}}
			accepted, hint, err := b.supportAnswerGrounded(context.Background(), message, messages, messages, test.answer, nil)
			if err != nil || accepted != test.want {
				t.Fatalf("accepted=%v hint=%q err=%v", accepted, hint, err)
			}
			if !test.want && (!strings.Contains(hint, "60 words") || !strings.Contains(hint, "safety warnings")) {
				t.Fatalf("rewrite lost the length budget or safety requirements: %q", hint)
			}
		})
	}
}

func TestSupportAgentRewritesVerboseDraftWithoutTruncating(t *testing.T) {
	for _, staysVerbose := range []bool{false, true} {
		t.Run(map[bool]string{false: "short rewrite", true: "still verbose"}[staysVerbose], func(t *testing.T) {
			const question = "when I set the album art cover to full screen and turn on immersive view, it just shows normally. is there a fix?"
			const short = "i don't have a confirmed fix for that. which Metrolist version and phone are you using?"
			long := "I couldn't find an existing report of that combination. " + strings.Repeat("unasked explanation ", 40)
			calls, checks := 0, 0
			b := &Bot{garminDecisions: supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				checks++
				if len(strings.Fields(request.State.(map[string]any)["proposed_answer"].(string))) > 60 {
					t.Fatal("verbose draft reached the paid evidence checker")
				}
				return supportTestDecision(request, "agent", .99, .99), nil
			})}
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if calls == 1 {
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: long}}, nil
				}
				hinted := false
				for _, message := range request.Messages {
					hinted = hinted || message.Role == "system" && strings.Contains(message.Content, "60 words")
					if strings.Contains(message.Content, long) {
						t.Fatal("undelivered verbose draft anchored the rewrite")
					}
				}
				if !hinted {
					t.Fatal("rewrite did not receive the brevity requirement")
				}
				answer := short
				if staysVerbose {
					answer = long
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: answer}}, nil
			})
			message := supportTestMessage(20)
			message.Content = question
			result, err := b.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: question}})
			if err != nil || calls != 2 {
				t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
			}
			if staysVerbose {
				if !result.Silent || !result.Unverified || result.Answer != "" || checks != 0 {
					t.Fatalf("repeatedly verbose draft was delivered or truncated: %+v checks=%d", result, checks)
				}
			} else if result.Silent || result.Answer != short || checks != 1 {
				t.Fatalf("short rewrite was lost: %+v checks=%d", result, checks)
			}
		})
	}
}

func TestSupportDestinationsRequireExactEvidence(t *testing.T) {
	sources := []string{"Use https://example.org/releases and #test-builds or <#123>."}
	for _, test := range []struct {
		answer string
		want   bool
	}{
		{"Open [releases](https://example.org/releases).", true},
		{"Ask in **#test-builds** or <#123>.", true},
		{"Open https://example.org/releases/secret", false},
		{"Ask in #made-up-channel.", false},
		{"Ask in <#999>.", false},
		{"Does it look correct in YouTube Music?", true},
	} {
		if got := supportDestinationsGrounded(test.answer, sources); got != test.want {
			t.Errorf("%q: grounded=%v", test.answer, got)
		}
	}
}

func TestUnreadAttachmentCannotRequestAnotherScreenshot(t *testing.T) {
	b := &Bot{garminDecisions: supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		t.Fatal("an unread-image request reached the model checker")
		return nil, nil
	})}
	m := supportTestMessage(20)
	m.Attachments = []discord.Attachment{{URL: "https://example.org/error.png"}}
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "I keep getting this error"}}
	accepted, hint, err := b.supportAnswerGrounded(context.Background(), m, messages, messages, "Please paste the exact error text or a screenshot here.", nil)
	if err != nil || accepted || !strings.Contains(hint, "exact error text") {
		t.Fatalf("accepted=%v hint=%q err=%v", accepted, hint, err)
	}
	if supportScreenshotRequestPattern.MatchString("I cannot read the screenshot. Paste the exact error text.") {
		t.Fatal("safe text clarification was blocked")
	}
}

func TestSupportAnswerGroundingFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name                    string
		probability             float64
		missing, apiError, want bool
	}{
		{name: "supported", probability: 0.99, want: true},
		{name: "threshold", probability: supportGroundedMin, want: true},
		{name: "uncertain", probability: supportGroundedMin - 0.01},
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
				if len(request.Questions) != 2 || request.Questions["grounded"].Type != "noul" || len(request.Questions["grounded"].Criteria) != 2 || request.Questions["problem"].Type != "choice" {
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
			if got, _, _ := b.supportAnswerGrounded(context.Background(), supportTestMessage(101), messages, conversation, "try a clean install", nil); got != test.want {
				t.Fatalf("grounded = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSupportNoteReferenceRequiresPositiveVerdict(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		use                            float64
		legacy, invalid, wantReference bool
	}{
		{name: "used", use: .99, wantReference: true},
		{name: "threshold", use: .95, wantReference: true},
		{name: "clarification", use: .01},
		{name: "uncertain", use: .94},
		{name: "invalid", use: 2, invalid: true},
		{name: "legacy has no reference check", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Bot{}
			if !tc.legacy {
				b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
					if request.State.(map[string]any)["reference_note"] != "rpc" || request.Questions["note_used"].Type != "noul" {
						t.Fatal("reference was not checked independently")
					}
					return supportTestDecision(request, "agent", .99, tc.use), nil
				})
			}
			name := "rpc"
			messages := []cmd.GarminAIMessage{{Role: "user", Content: "How do I connect Discord?"}}
			accepted, _, err := b.supportAnswerGrounded(context.Background(), supportTestMessage(20), messages, messages, "Which Metrolist variant are you using?", &name)
			if tc.invalid {
				if err == nil || accepted {
					t.Fatal("invalid reference verdict did not fail closed")
				}
				return
			}
			if err != nil || !accepted || (name != "") != tc.wantReference {
				t.Fatalf("accepted=%v reference=%q err=%v", accepted, name, err)
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
			if _, _, err := b.supportAnswerGrounded(context.Background(), m, messages, messages, "Please paste the error text.", nil); err != nil {
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
