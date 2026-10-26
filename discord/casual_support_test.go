package discord

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestCasualAppGuidanceUsesIsolatedCheckedSupport(t *testing.T) {
	for _, appGuidance := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary chat", true: "app followup"}[appGuidance], func(t *testing.T) {
			b := &Bot{}
			classified, checks, calls := 0, 0, 0
			b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				if _, ok := request.Questions["app_guidance"]; ok {
					classified++
					state := request.State.(supportDecisionState)
					if state.Message != "garmin, yes it does" || len(state.Conversation) != 2 {
						t.Fatalf("classifier lost the keyword-free follow-up: %+v", state)
					}
					p := 0.1
					if appGuidance {
						p = .99
					}
					return casualGuidanceTestDecision(t, p), nil
				}
				checks++
				skill := request.State.(map[string]any)["support_skill"].(string)
				for _, name := range []string{"rpc", "login", "scrobbling", "playlists", "discovery"} {
					guide, _ := loadGarminSupportGuide(name)
					if !strings.Contains(skill, guide) {
						t.Fatalf("checker lost %s", name)
					}
				}
				if checks == 1 {
					return supportTestDecision(request, "agent", .1, .1), nil
				}
				return supportTestDecision(request, "agent", .99, .99), nil
			})
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				calls++
				if appGuidance {
					if !strings.HasPrefix(request.SystemPrompt, garminAppSupportSystemPrompt) || strings.Contains(request.Context, "PRIVATE_PROFILE") || garminToolAvailable(request.Tools, "get_discord_profile") || garminToolAvailable(request.Tools, "search_web") {
						t.Fatal("casual app guidance inherited persona/profile or unrelated tools")
					}
					if calls == 1 {
						return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "REJECTED_APP_CLAIM"}}, nil
					}
					if calls == 2 {
						return garminHistoryTestCall("load_skill", `{"name":"support"}`), nil
					}
					for _, turn := range request.Messages {
						if strings.Contains(turn.Content, "REJECTED_APP_CLAIM") {
							t.Fatal("rejected draft anchored the retry")
						}
					}
				} else if !strings.HasPrefix(request.SystemPrompt, cmd.GarminSystemPrompt()) {
					t.Fatal("ordinary chat lost its normal prompt")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Which app variant and platform are you using?"}}, nil
			})
			m := supportTestMessage(44)
			m.ChannelID = snowflakeID(garminBotsID)
			m.Author.Username = "PRIVATE_PROFILE"
			m.Content = "garmin, yes it does"
			messages := []cmd.GarminAIMessage{{Role: "user", Content: "Can I sign into Metrolist?"}, {Role: "assistant", Content: "Login is unsupported."}, {Role: "user", Content: "yes it does"}}
			result, err := b.runGarminAI(context.Background(), nil, m, messages)
			if err != nil || result == nil || result.Silent || classified != 1 || (appGuidance && (checks != 2 || calls != 3)) || (!appGuidance && (checks != 0 || calls != 1)) {
				t.Fatalf("result=%+v err=%v classified=%d checks=%d calls=%d", result, err, classified, checks, calls)
			}
		})
	}
}

func TestCasualGuidanceClassifiesRetainedTopicBeforeCompaction(t *testing.T) {
	for _, summarized := range []bool{false, true} {
		t.Run(map[bool]string{false: "earlier topic", true: "retained summary"}[summarized], func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			classified, compacted := 0, false
			b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				if _, ok := request.Questions["app_guidance"]; ok {
					classified++
					state, _ := json.Marshal(request.State)
					if !strings.Contains(string(state), "Metrolist login") || strings.Contains(string(state), "PRIVATE_PROFILE") || strings.Contains(string(state), "discord_") || strings.Contains(string(state), "1242567443742986373") || strings.Contains(string(state), "Retained conversation participants") {
						t.Fatal("classifier lost the retained subject or received profile metadata")
					}
					return casualGuidanceTestDecision(t, .99), nil
				}
				return supportTestDecision(request, "agent", .99, .99), nil
			})
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if classified != 1 || strings.Contains(request.Context, "PRIVATE_PROFILE") {
					t.Fatal("compaction/generation ran before classification or inherited profile metadata")
				}
				if request.SystemPrompt == garminAIConversationSummaryPrompt {
					compacted = true
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: `{"summary":"The user is correcting a Metrolist login denial."}`}}, nil
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "You're right. Login is supported in standalone Android and KMP."}}, nil
			})
			first := cmd.GarminAIMessage{Role: "user", Content: "Metrolist login is supported."}
			count := 19
			if summarized {
				producer := &Bot{garminAI: garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: `{"summary":"Metrolist login was discussed by discord_1242567443742986373 (<@1242567443742986373>, ID 1242567443742986373)."}`}}, nil
				})}
				named := make([]cmd.GarminAIMessage, garminAICompactionMessages+1)
				for i := range named {
					named[i] = cmd.GarminAIMessage{Role: "user", Name: "discord_1242567443742986373", Content: "Metrolist login"}
				}
				compacted, err := producer.compactGarminAIConversation(context.Background(), named, "")
				if err != nil {
					t.Fatal(err)
				}
				first = compacted[0]
				count++
			}
			messages := []cmd.GarminAIMessage{first}
			for range count {
				messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: "one more detail"})
			}
			messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: "yes it does"})
			m := supportTestMessage(46)
			m.ChannelID = snowflakeID(garminBotsID)
			m.Author.Username = "PRIVATE_PROFILE"
			m.Content = "garmin, yes it does"
			b.handleGarminAI(client, m, messages)
			if classified != 1 || !compacted || sent.count() != 1 {
				t.Fatalf("classified=%d compacted=%v sends=%d", classified, compacted, sent.count())
			}
		})
	}
}

func TestCasualGuidanceClassificationFailureStopsGeneration(t *testing.T) {
	for _, failure := range []string{"api", "invalid"} {
		t.Run(failure, func(t *testing.T) {
			b := &Bot{
				garminAI: garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					t.Fatal("generated an unchecked casual answer after classification failure")
					return nil, nil
				}),
				garminDecisions: supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
					if failure == "api" {
						return nil, errors.New("unavailable")
					}
					return nil, nil
				}),
			}
			m := supportTestMessage(45)
			m.ChannelID = snowflakeID(garminBotsID)
			if _, err := b.runGarminAI(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: "hello"}}); err == nil {
				t.Fatal("classification failure was ignored")
			}
		})
	}
}

func casualGuidanceTestDecision(t *testing.T, probability float64) *cmd.DecisionResponse {
	t.Helper()
	var response cmd.DecisionResponse
	if err := json.Unmarshal([]byte(`{"model":"test/model","usage":{"input_tokens":1,"output_tokens":1},"answers":{"app_guidance":{"type":"noul","noul":0}}}`), &response); err != nil {
		t.Fatal(err)
	}
	response.Answers["app_guidance"] = cmd.DecisionAnswer{Type: "noul", Noul: &probability}
	return &response
}
