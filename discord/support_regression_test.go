package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	gh "github.com/MetrolistGroup/metrobot/github"
	"github.com/MetrolistGroup/metrobot/internal/notefmt"
)

func TestSupportFollowupWithoutReplyKeepsUserContext(t *testing.T) {
	b, client, _ := newSupportDecisionTestBot(t)
	b.garminAI = &fakeGarminAI{}
	original := supportTestMessage(10)
	history := []cmd.GarminAIMessage{
		{Role: "user", Content: "Stable and nightly crash on my Honor phone; debug works."},
		{Role: "assistant", Content: "What Android version and exact error do you see?"},
	}
	b.garminAIMu.Lock()
	b.storeGarminAIContextLocked("11", original, history, time.Now().Add(-19*time.Minute))
	b.garminAIMu.Unlock()
	requests := make(chan supportDecisionState, 1)
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		requests <- request.State.(supportDecisionState)
		return supportTestDecision(request, "none", .01, .99), nil
	})
	followup := supportTestMessage(20)
	followup.Content = "android 16 and security from September 2026"
	b.onMessageCreate(client, followup)
	select {
	case state := <-requests:
		if state.Message != followup.Content || len(state.Conversation) != 2 || state.Conversation[0].Content != history[0].Content || state.Parent != "" {
			t.Fatalf("standalone follow-up lost support history: %#v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("standalone follow-up was not classified")
	}
}

func TestSupportCompactionDoesNotWriteSharedMemory(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := memory.Read()
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{garminMemory: memory, garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if !strings.Contains(request.Context, "timestamps only") || strings.Contains(request.Context, "PRIVATE_PROFILE") || strings.Contains(request.Context, "current_user") {
			t.Fatal("support compaction inherited Discord profile context")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: `{"summary":"Crash investigation remains unresolved","shared_memory":["Metrolist release notes use Markdown"]}`}}, nil
	})}
	messages := make([]cmd.GarminAIMessage, garminAICompactionMessages+1)
	for i := range messages {
		messages[i] = cmd.GarminAIMessage{Role: "user", Content: "support detail"}
	}
	m := supportTestMessage(101)
	m.Author.Username = "PRIVATE_PROFILE"
	got := b.prepareGarminAIConversation(context.Background(), nil, m, messages)
	after, err := memory.Read()
	if err != nil || after != before || len(got) >= len(messages) {
		t.Fatal("support compaction failed or changed shared memory")
	}
}

func TestSupportSendsCheckedExcerptNotChangedOrFullNote(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "automatic"}[automatic], func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			const description = "How to get nightly builds"
			if err := b.Notes.EditNote("nightly", description, notefmt.Fence("faq", testKMPNote)); err != nil {
				t.Fatal(err)
			}
			const answer = "Relevant KMP excerpt."
			b.garminAI = supportTestNoteAnswer("nightly", answer)
			b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				state := request.State.(map[string]any)
				sources, _ := json.Marshal(state["tool_results"])
				if state["proposed_answer"] != answer || !strings.Contains(string(sources), "KMP") {
					t.Fatal("checker lost the excerpt or its original source")
				}
				if err := b.Notes.EditNote("nightly", description, "CHANGED_AFTER_CHECK"); err != nil {
					t.Fatal(err)
				}
				return supportTestDecision(request, "agent", .99, .99), nil
			})
			m := supportTestMessage(20)
			messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
			if automatic {
				if !b.runSupportDecisionAgent(client, m, messages, "nightly") {
					t.Fatal("automatic snapshot delivery failed")
				}
			} else {
				b.handleGarminAI(client, m, messages)
			}
			if sent.count() != 1 {
				t.Fatalf("sent %d replies", sent.count())
			}
			sent.mu.Lock()
			body := sent.bodies[0]
			sent.mu.Unlock()
			if strings.Contains(body, "CHANGED_AFTER_CHECK") || !strings.Contains(body, "components") || !strings.Contains(body, "KMP") {
				t.Fatalf("checked rich note snapshot lost: %s", body)
			}
			stored, ok := b.garminAIHistory(m, "", false)
			if !ok || stored[len(stored)-1].Content != answer {
				t.Fatal("retained text differs from checked note")
			}
		})
	}
}

func TestSupportToolFailureDoesNotExposeUncheckedNoteReference(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	b.garminGitHub = gh.NewAssistantClient("", "MetrolistGroup", "Metrolist")
	original := http.DefaultTransport
	http.DefaultTransport = garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	calls, checks := 0, 0
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if calls > 1 {
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "What exact crash error do you see?"}}, nil
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{
			ID: "lookup", Type: "function", Function: cmd.GarminAIFunctionCall{Name: "get_metrolist_status", Arguments: `{}`},
		}}}}, nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		checks++
		response := supportTestDecision(request, "agent", .99, .99)
		zero := 0.0
		response.Answers["note_used"] = cmd.DecisionAnswer{Type: "noul", Noul: &zero}
		return response, nil
	})
	ctx := context.WithValue(context.Background(), supportSelectedNoteContextKey{}, "nightly")
	result, err := b.runGarminAI(ctx, nil, supportTestMessage(20), []cmd.GarminAIMessage{{Role: "user", Content: "Does nightly fix this crash?"}})
	if err != nil || result == nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Silent || result.Answer != "" || result.NoteName != "" || calls != 1 || checks != 0 {
		t.Fatalf("tool failure exposed an unchecked reference: answer=%q note=%q", result.Answer, result.NoteName)
	}
}

func TestSupportRewriteOnlySendsAndRetainsAcceptedAnswer(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	calls, checks := 0, 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		answer := "UNSUPPORTED_DRAFT"
		if calls == 2 {
			for _, message := range request.Messages {
				if message.Content == "UNSUPPORTED_DRAFT" {
					t.Fatal("rewrite remained anchored to a rejected draft")
				}
			}
			correction := request.Messages[len(request.Messages)-1]
			if correction.Role != "system" || !strings.Contains(correction.Content, "using the evidence already loaded") || !strings.Contains(correction.Content, "If one missing detail changes the answer") {
				t.Fatal("rewrite lost evidence correction or unnecessarily requires a variant")
			}
			return garminHistoryTestCall("load_skill", `{"name":"support"}`), nil
		}
		if calls == 3 {
			answer = "Please paste the exact error."
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: answer}}, nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		checks++
		probability := .01
		if checks == 2 {
			probability = .99
		}
		return supportTestDecision(request, "agent", probability, probability), nil
	})
	m := supportTestMessage(20)
	b.handleGarminAI(client, m, []cmd.GarminAIMessage{{Role: "user", Content: "I get an error"}})
	if calls != 3 || checks != 2 || sent.count() != 1 {
		t.Fatalf("calls=%d checks=%d sends=%d", calls, checks, sent.count())
	}
	b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
	b.handleGarminAI(client, m, []cmd.GarminAIMessage{{Role: "user", Content: "I get an error"}})
	if calls != 3 || sent.count() != 1 {
		t.Fatal("duplicate explicit support event was answered twice")
	}
	history, ok := b.garminAIHistory(m, "", false)
	if !ok || len(history) != 2 || history[1].Content != "Please paste the exact error." {
		t.Fatal("rejected draft leaked into retained conversation")
	}
	sent.mu.Lock()
	defer sent.mu.Unlock()
	if strings.Contains(sent.bodies[0], "UNSUPPORTED_DRAFT") {
		t.Fatal("rejected draft was sent")
	}
}

func TestSupportChecksFullLongNoteSnapshot(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	body := strings.Repeat("approved text ", 1100) + "APPROVED_TAIL"
	if len(body) <= garminAIToolResultSize {
		t.Fatal("fixture must exceed tool truncation limit")
	}
	if err := b.Notes.EditNote("nightly", "How to get nightly builds", body); err != nil {
		t.Fatal(err)
	}
	b.garminAI = supportTestNoteAnswer("nightly", "APPROVED_TAIL")
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		state := request.State.(map[string]any)
		sources, err := json.Marshal(state["tool_results"])
		if err != nil || !strings.Contains(string(sources), "APPROVED_TAIL") {
			t.Fatal("checker lost the note's tail")
		}
		if state["proposed_answer"] != "APPROVED_TAIL" {
			t.Fatal("candidate is not the requested excerpt")
		}
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	m := supportTestMessage(20)
	result, err := b.runGarminAI(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if err != nil || result.NoteName != "nightly" || result.Answer != "APPROVED_TAIL" {
		t.Fatalf("excerpt from full source lost: %v, %+v", err, result)
	}
}

func TestExplicitSupportEvidenceErrorStaysSilentWithoutRewrite(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	calls := 0
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "candidate answer"}}, nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(context.Context, cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		return nil, errors.New("evidence API unavailable")
	})
	m := supportTestMessage(20)
	b.handleGarminAI(client, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if calls != 1 || sent.count() != 0 {
		t.Fatalf("API failure generated %d calls and %d sends", calls, sent.count())
	}
	retained := b.garminAIUserContexts[garminAIUserContextKey(m)]
	if len(retained.messages) != 1 || retained.messages[0].Content != m.Content || !retained.unanswered || !retained.ambientUntil.IsZero() {
		t.Fatal("API failure lost pending state, retained a draft or enabled ambient replies")
	}
}
