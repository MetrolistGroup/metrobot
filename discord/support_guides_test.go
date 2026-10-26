package discord

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestSupportSetupGuidesReachAgentAndEvidenceCheck(t *testing.T) {
	full, err := loadGarminSkill("support")
	if err != nil {
		t.Fatal(err)
	}
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "How do I connect Discord?"}}
	selected, err := loadGarminSupportAnswerSkill()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rpc", "login", "scrobbling", "playlists"} {
		guide, err := loadGarminSupportGuide(name)
		if err != nil || !strings.Contains(full, guide) || strings.Contains(selected, guide) {
			t.Fatalf("wrong guide selection/evidence for %s: %v", name, err)
		}
	}
	checked, calls := false, 0
	messages = append(messages, cmd.GarminAIMessage{Role: "assistant", Content: "Which variant?"}, cmd.GarminAIMessage{Role: "user", Content: "regular Android 13.7.0"})
	b := &Bot{
		garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			calls++
			if !strings.Contains(request.SystemPrompt, selected) || strings.Contains(request.SystemPrompt, full) {
				t.Fatal("agent must start with the dedicated support core only")
			}
			if strings.Contains(request.Context, "public search") || strings.Contains(request.Context, "PRIVATE_PROFILE") {
				t.Fatal("support inherited unrelated web-search or personal profile context")
			}
			if !garminToolAvailable(request.Tools, "load_skill") || !garminToolAvailable(request.Tools, "load_support_guide") {
				t.Fatal("dedicated support core removed static skill/guide discovery")
			}
			if calls == 1 {
				return garminHistoryTestCall("load_support_guide", `{"name":"rpc"}`), nil
			}
			guide, _ := loadGarminSupportGuide("rpc")
			if request.Messages[len(request.Messages)-1].Content != guide {
				t.Fatal("model-selected guide did not reach synthesis")
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Open the account menu, Integrations, then Discord. Log in and enable Rich Presence."}}, nil
		}),
		garminDecisions: supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
			checked = true
			if request.State.(map[string]any)["support_skill"] != full {
				t.Fatal("checker lost the full trusted reference set")
			}
			return supportTestDecision(request, "agent", .99, .99), nil
		}),
	}
	m := supportTestMessage(20)
	m.Content = messages[len(messages)-1].Content
	m.Author.Username = "PRIVATE_PROFILE"
	result, err := b.runGarminAI(context.Background(), nil, m, messages)
	if err != nil || !checked || result.Silent || calls != 2 {
		t.Fatalf("guide-backed answer failed: %v, %+v", err, result)
	}
}

func TestSupportCanRetrieveGuideWithoutTopicKeywords(t *testing.T) {
	guide, err := loadGarminSupportGuide("rpc")
	if err != nil {
		t.Fatal(err)
	}
	calls, checked := 0, false
	b := &Bot{
		garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			calls++
			if calls == 1 {
				if strings.Contains(request.SystemPrompt, guide) || !garminToolAvailable(request.Tools, "load_support_guide") {
					t.Fatal("unknown wording must retain a guide retrieval path")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{ID: "guide", Type: "function", Function: cmd.GarminAIFunctionCall{Name: "load_support_guide", Arguments: `{"name":"rpc"}`}}}}}, nil
			}
			if request.Messages[len(request.Messages)-1].Content != guide {
				t.Fatal("retrieval lost guide text or scope")
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Open Integrations, choose Discord, log in and enable Rich Presence."}}, nil
		}),
		garminDecisions: supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
			checked = true
			sources, _ := json.Marshal(request.State.(map[string]any)["tool_results"])
			if !strings.Contains(string(sources), `"tool":"load_support_guide"`) || !strings.Contains(string(sources), "Discord Rich Presence (RPC) setup reference") {
				t.Fatal("checker lost guide retrieval provenance")
			}
			return supportTestDecision(request, "agent", .99, .99), nil
		}),
	}
	m := supportTestMessage(21)
	m.Content = "Comment afficher ma musique sur mon profil ?"
	result, err := b.runGarminAI(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if err != nil || result == nil || result.Silent || calls != 2 || !checked {
		t.Fatalf("guide fallback failed: result=%+v calls=%d checked=%v err=%v", result, calls, checked, err)
	}
}
