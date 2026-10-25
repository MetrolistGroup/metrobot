package discord

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
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
	b := &Bot{garminMemory: memory, garminAI: &summaryGarminAI{summary: `{"summary":"Crash investigation remains unresolved","shared_memory":["Metrolist release notes use Markdown"]}`}}
	messages := make([]cmd.GarminAIMessage, garminAICompactionMessages+1)
	for i := range messages {
		messages[i] = cmd.GarminAIMessage{Role: "user", Content: "support detail"}
	}
	got := b.prepareGarminAIConversation(context.Background(), nil, supportTestMessage(101), messages)
	after, err := memory.Read()
	if err != nil || after != before || len(got) >= len(messages) {
		t.Fatal("support compaction failed or changed shared memory")
	}
}

func TestSupportSendsCheckedNoteSnapshot(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "automatic"}[automatic], func(t *testing.T) {
			b, client, sent := newSupportDecisionTestBot(t)
			const description = "How to get nightly builds"
			if err := b.Notes.EditNote("nightly", description, notefmt.Fence("faq", testKMPNote)); err != nil {
				t.Fatal(err)
			}
			b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				return garminHistoryTestCall("get_note", `{"name":"nightly"}`), nil
			})
			b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
				if err := b.Notes.EditNote("nightly", description, "CHANGED_AFTER_CHECK"); err != nil {
					t.Fatal(err)
				}
				return supportTestDecision(request, "agent", .99, .99), nil
			})
			m := supportTestMessage(20)
			messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
			if automatic {
				if !b.runSupportDecisionAgent(client, m, messages) {
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
			if !ok || stored[len(stored)-1].Content != testKMPNote {
				t.Fatal("retained text differs from checked note")
			}
		})
	}
}

func TestSupportRewriteOnlySendsAndRetainsAcceptedAnswer(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	calls, checks := 0, 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		answer := "UNSUPPORTED_DRAFT"
		if calls == 2 {
			if request.Messages[len(request.Messages)-1].Role != "system" {
				t.Fatal("rewrite lost evidence correction")
			}
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
	if calls != 2 || checks != 2 || sent.count() != 1 {
		t.Fatalf("calls=%d checks=%d sends=%d", calls, checks, sent.count())
	}
	b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
	b.handleGarminAI(client, m, []cmd.GarminAIMessage{{Role: "user", Content: "I get an error"}})
	if calls != 2 || sent.count() != 1 {
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
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return garminHistoryTestCall("get_note", `{"name":"nightly"}`), nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		state := request.State.(map[string]any)
		sources, err := json.Marshal(state["tool_results"])
		if err != nil || !strings.Contains(string(sources), "APPROVED_TAIL") {
			t.Fatal("checker lost the note's tail")
		}
		if state["proposed_answer"] != body {
			t.Fatal("candidate was truncated")
		}
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	m := supportTestMessage(20)
	result, err := b.runGarminAI(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if err != nil || result.NoteSnapshot == nil || result.NoteSnapshot.Text != body {
		t.Fatal("approved long snapshot lost")
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
}
