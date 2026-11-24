package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/MetrolistGroup/metrobot/config"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	gh "github.com/MetrolistGroup/metrobot/github"
)

func supportNoulDecision(name string, p float64) *cmd.DecisionResponse {
	var response cmd.DecisionResponse
	_ = json.Unmarshal([]byte(mustJSON(map[string]any{
		"model": "test/model", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
		"answers": map[string]any{name: map[string]any{"type": "noul", "noul": p}},
	})), &response)
	return &response
}

func TestConfirmedSupportFixIsSavedOnlyForTheAsker(t *testing.T) {
	for _, test := range []struct {
		name  string
		asker string
		p     float64
		want  bool
	}{
		{"asker confirms", garminAIMessageName("42"), .97, true},
		{"bystander confirms", garminAIMessageName("43"), .97, false},
		{"unclear", garminAIMessageName("42"), .5, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, _, _ := newSupportDecisionTestBot(t)
			b.garminDecisions = supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				return supportNoulDecision("confirms_fix", test.p), nil
			})
			m := supportTestMessage(30)
			m.Content = "that worked, thanks"
			messages := []cmd.GarminAIMessage{
				{Role: "user", Name: test.asker, Content: "how do I translate lyrics?"},
				{Role: "assistant", Content: "Open lyrics, then tap Translate."},
				{Role: "user", Name: garminAIMessageName("42"), Content: m.Content},
			}
			b.recordConfirmedSupportFix(context.Background(), m, messages)
			note, err := b.Notes.DB.GetNote(supportConfirmedFixesNote)
			if err != nil || strings.Contains(note, "tap Translate") != test.want {
				t.Fatalf("note=%q err=%v want saved=%v", note, err, test.want)
			}
		})
	}
}

func TestConfirmedSupportFixDoesNotOutliveCancellationOrReset(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint("reset=", reset), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, _, _ := newSupportDecisionTestBot(t)
			m := supportTestMessage(30)
			m.Content = "that worked, thanks"
			b.garminDecisions = supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				if reset {
					b.garminAIMu.Lock()
					b.garminContextCutoffs = map[string]string{m.ChannelID.String(): m.ID.String()}
					b.garminAIMu.Unlock()
				} else {
					cancel()
				}
				return supportNoulDecision("confirms_fix", .99), nil
			})
			messages := []cmd.GarminAIMessage{
				{Role: "user", Name: garminAIMessageName(m.Author.ID.String()), Content: "how do I translate lyrics?"},
				{Role: "assistant", Content: "Open lyrics, then tap Translate."},
				{Role: "user", Content: m.Content},
			}
			b.recordConfirmedSupportFix(ctx, m, messages)
			if note, err := b.Notes.DB.GetNote(supportConfirmedFixesNote); err != nil || note != "" {
				t.Fatalf("canceled or reset request persisted support history: %q err=%v", note, err)
			}
		})
	}
}

func TestConfirmedSupportFixesStayNewestFirstAndBounded(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		answer := strings.Repeat("x", 600) + string(rune('A'+i))
		if err := b.appendConfirmedSupportFix("problem", answer, day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.appendConfirmedSupportFix("problem", strings.Repeat("x", 600)+"^", day); err != nil { // 'A'+29 duplicate
		t.Fatal(err)
	}
	note, _ := b.Notes.DB.GetNote(supportConfirmedFixesNote)
	if len(note) > supportConfirmedFixesMax || !strings.HasPrefix(note, "### 2026-10-30\n") || strings.Contains(note, "### 2026-10-01\n") || strings.Count(note, "### 2026-10-30") != 1 {
		t.Fatalf("note has %d bytes, starts %q", len(note), note[:40])
	}
}

func TestSupportRetryCarriesCheckerHint(t *testing.T) {
	checks := 0
	b := &Bot{Config: testConfig(config.Config{})}
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		checks++
		response := supportNoulDecision("grounded", .01)
		if checks > 1 {
			response = supportNoulDecision("grounded", .99)
		}
		problem := "needs_research"
		if checks > 1 {
			problem = "none"
		}
		response.Answers["problem"] = cmd.DecisionAnswer{Type: "choice", Choice: problem}
		return response, nil
	})
	var hinted bool
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		for _, message := range request.Messages {
			hinted = hinted || message.Role == "system" && strings.Contains(message.Content, supportRetryHints["needs_research"])
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "draft"}}, nil
	})
	result, err := b.runGarminAI(context.Background(), nil, supportTestMessage(20), []cmd.GarminAIMessage{{Role: "user", Content: "how do I translate lyrics?"}})
	if err != nil || !hinted || result.Answer != "draft" || checks != 2 {
		t.Fatalf("result=%+v err=%v hinted=%v checks=%d", result, err, hinted, checks)
	}
}

func TestConfirmedSupportFixFollowsDeliveredAnswer(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		if _, ok := request.Questions["confirms_fix"]; ok {
			return supportNoulDecision("confirms_fix", .97), nil
		}
		if _, routing := request.Questions["route"]; routing && strings.Contains(mustJSON(request.State), "that worked") {
			return supportTestDecision(request, "none", .1, .99), nil
		}
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Open lyrics, then tap Translate."}}, nil
	})
	m := supportTestMessage(20)
	m.Content = "how do I translate lyrics?"
	b.handleGarminAutomaticAppSupport(client, m, []cmd.GarminAIMessage{garminAIUserMessage(m, m.Content)})
	if sent.count() != 1 {
		t.Fatal("answer was not delivered")
	}
	followup := supportTestMessage(21)
	followup.Content = "that worked, thanks"
	b.handleGarminAutomaticAppSupport(client, followup, b.garminAITriggeredConversation(followup, followup.Content))
	note, _ := b.Notes.DB.GetNote(supportConfirmedFixesNote)
	if !strings.Contains(note, "Confirmed answer: Open lyrics, then tap Translate.") || !strings.Contains(note, "how do I translate lyrics?") || sent.count() != 1 {
		t.Fatalf("confirmation was not learned: note=%q sent=%d", note, sent.count())
	}
}

func TestSupportEvidenceCheckFitsLargeResearch(t *testing.T) {
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "search shows no results"}}
	conversation := copyGarminAIMessages(messages)
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("call-%d", i)
		conversation = append(conversation,
			cmd.GarminAIMessage{Role: "assistant", ToolCalls: []cmd.GarminAIToolCall{{ID: id, Function: cmd.GarminAIFunctionCall{Name: "get_metrolist_issue", Arguments: `{"number":1}`}}}},
			cmd.GarminAIMessage{Role: "tool", ToolCallID: id, Content: strings.Repeat("x", 12<<10)})
	}
	b := &Bot{Config: testConfig(config.Config{}), garminDecisions: supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		if body, _ := json.Marshal(request); len(body) > cmd.DecisionsBodyMax-512 {
			t.Fatalf("evidence request is %d bytes", len(body))
		}
		sources, _ := json.Marshal(request.State.(map[string]any)["tool_results"])
		if strings.Count(string(sources), `"tool":"get_metrolist_issue"`) != 1 {
			t.Fatal("checker resends identical evidence")
		}
		return supportNoulDecision("grounded", .9), nil
	})}
	if accepted, _, err := b.supportAnswerGrounded(context.Background(), supportTestMessage(5), messages, conversation, "draft", nil); err != nil || !accepted {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
}

func TestSupportResearchToolBudget(t *testing.T) {
	b := &Bot{Config: testConfig(config.Config{}), garminDecisions: supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		return supportNoulDecision("grounded", .99), nil
	})}
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"total_count":0,"items":[]}`)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	b.garminGitHub = gh.NewAssistantClient("synthetic", "MetrolistGroup", "Metrolist")
	var refused int
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		refused = 0
		for _, message := range request.Messages {
			if message.Role == "tool" && strings.Contains(message.Content, "budget used") {
				refused++
			}
		}
		if refused == 0 && request.Tools != nil {
			calls := make([]cmd.GarminAIToolCall, garminSupportResearchBudget+1)
			for i := range calls {
				calls[i] = cmd.GarminAIToolCall{ID: fmt.Sprint(i), Type: "function", Function: cmd.GarminAIFunctionCall{Name: "search_metrolist_code", Arguments: `{"query":"lyrics translation"}`}}
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: calls}}, nil
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "answer"}}, nil
	})
	result, err := b.runGarminAI(context.Background(), nil, supportTestMessage(6), []cmd.GarminAIMessage{{Role: "user", Content: "how do I translate lyrics?"}})
	if err != nil || result.Answer != "answer" || refused != 1 {
		t.Fatalf("result=%+v err=%v refused=%d", result, err, refused)
	}
}

func TestConfirmedSupportFixStoresWholeAnswersOnce(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	answer := strings.Repeat("Keep the existing app settings unchanged. ", 40) + "SAFETY_WARNING: back up before reinstalling; this removes local data."
	for range 2 {
		if err := b.appendConfirmedSupportFix("reinstall issue", answer, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := b.Notes.DB.GetNote(supportConfirmedFixesNote)
	if err != nil || strings.Count(saved, "Reported problem") != 1 || !strings.Contains(saved, answer) {
		t.Fatalf("saved=%q err=%v", saved, err)
	}
	oversized := strings.Repeat("y", supportConfirmedAnswerMax) + " SAFETY_WARNING"
	if err := b.appendConfirmedSupportFix("other issue", oversized, time.Now()); err != nil {
		t.Fatal(err)
	}
	if after, _ := b.Notes.DB.GetNote(supportConfirmedFixesNote); after != saved {
		t.Fatal("oversized answer was stored clipped")
	}
}

func TestConfirmedSupportFixEvictionKeepsAnswerHeadingsAndWarnings(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	var answers []string
	for index := range 20 {
		answer := fmt.Sprintf("Answer %d: %s\n### Warning\nBack up first. SAFETY-%d", index, strings.Repeat("Keep the existing settings. ", 45), index)
		answers = append(answers, answer)
		if err := b.appendConfirmedSupportFix("installation", answer, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := b.Notes.DB.GetNote(supportConfirmedFixesNote)
	if err != nil || len(saved) > supportConfirmedFixesMax || !strings.Contains(saved, answers[len(answers)-1]) {
		t.Fatalf("confirmed fixes were not saved within their budget: %v", err)
	}
	for index, answer := range answers {
		if strings.Contains(saved, fmt.Sprintf("Confirmed answer: Answer %d:", index)) && !strings.Contains(saved, answer) {
			t.Fatalf("answer %d was retained without its complete warning", index)
		}
	}
}
