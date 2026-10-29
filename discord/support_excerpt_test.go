package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
)

func TestSupportNoteSectionsPreserveScopeAndCode(t *testing.T) {
	source := "For standalone Android only.\n\n## First\nStep one. Back up first.\n```text\n## Not a section\n```\n\n## Second\nStep two."
	sections := supportNoteSections(source)
	if len(sections) != 2 || !strings.HasPrefix(sections[0], "For standalone Android only.") || !strings.Contains(sections[0], "Back up first.") || !strings.Contains(sections[0], "## Not a section") || sections[1] != "For standalone Android only.\n\n## Second\nStep two." {
		t.Fatalf("sections=%q", sections)
	}
	if got := supportNoteSections("plain note\nwith a link"); len(got) != 1 || got[0] != "plain note\nwith a link" {
		t.Fatalf("plain note changed: %q", got)
	}
	if got := supportNoteSections(" \n"); len(got) != 0 {
		t.Fatal("empty note became an answer")
	}
}

func excerptDecision(request cmd.DecisionRequest, selected string, probability float64) *cmd.DecisionResponse {
	response := supportNoulDecision("unused", 1)
	weights := make(map[string]*float64)
	for option := range request.Questions["section"].Criteria {
		p := (1 - probability) / float64(len(request.Questions["section"].Criteria)-1)
		if option == selected {
			p = probability
		}
		weights[option] = &p
	}
	response.Answers = map[string]cmd.DecisionAnswer{"section": {Type: "choice", Choice: selected, Probabilities: weights}}
	return response
}

func TestSupportExcerptSendsRetrievedSnapshotWithoutParaphrasing(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	b.Config.OpenRouterDecisionModel = "qwen/qwen3.7-flash"
	const body = "## Released?\nNot yet. No date is set.\n\n## Access?\nAsk staff about access."
	if err := b.Notes.AddNote("release", "Release status and access", body); err != nil {
		t.Fatal(err)
	}
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		t.Fatal("authored answer was sent through free-form generation")
		return nil, nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		if request.Questions["section"].Type != "choice" {
			t.Fatal("unexpected extra evidence call")
		}
		if err := b.Notes.EditNote("release", "Release status", "A later edit."); err != nil {
			t.Fatal(err)
		}
		return excerptDecision(request, "s0", .99), nil
	})
	m := supportTestMessage(20)
	m.Content = "Is it released?"
	ctx := context.WithValue(context.Background(), supportSelectedNoteContextKey{}, "release")
	result, err := b.runGarminAI(ctx, nil, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if err != nil || result.Answer != "## Released?\nNot yet. No date is set." || result.NoteName != "release" || result.Unverified {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestQwenTentativeNoteStillNeedsApplicabilitySelection(t *testing.T) {
	for _, matches := range []bool{true, false} {
		b, client, sent := newSupportDecisionTestBot(t)
		b.Config.OpenRouterDecisionModel = "qwen/qwen3.7-flash"
		selections := 0
		b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
			if request.Questions["route"].Type != "" {
				return supportTestDecision(request, "nightly", .99, .6), nil
			}
			selections++
			choice := "agent"
			if matches {
				choice = "s0"
			}
			return excerptDecision(request, choice, .99), nil
		})
		b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			if matches {
				t.Fatal("tentative matching note was discarded before inspection")
			}
			return garminHistoryTestCall("do_not_respond", `{}`), nil
		})
		m := supportTestMessage(20)
		b.handleGarminSupportDecision(client, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
		if selections != 1 || (sent.count() == 1) != matches {
			t.Fatalf("matches=%v selections=%d deliveries=%d", matches, selections, sent.count())
		}
	}
}

func TestQwenCannotApproveParaphrasedNoteFacts(t *testing.T) {
	b := &Bot{Config: &config.Config{OpenRouterDecisionModel: "qwen/qwen3.7-flash"}}
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		return supportTestDecision(request, "agent", 1, 1), nil
	})
	const source = "## Artwork\nDoes the album look correct in YouTube Music?"
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "Why does only one song have a cover?"}}
	conversation := append([]cmd.GarminAIMessage{}, messages...)
	lookup := garminHistoryTestCall("get_note", `{"name":"uploads"}`).Message
	lookup.Role = "assistant"
	conversation = append(conversation, lookup)
	call := conversation[len(conversation)-1].ToolCalls[0]
	conversation = append(conversation, cmd.GarminAIMessage{Role: "tool", ToolCallID: call.ID, Content: source})
	for _, answer := range []string{source, "Metrolist applies artwork per track. Does it look correct in YouTube Music?"} {
		note := "uploads"
		accepted, hint, err := b.supportAnswerGrounded(context.Background(), supportTestMessage(20), messages, conversation, answer, &note)
		if err != nil || accepted != (answer == source) || (!accepted && !strings.Contains(hint, "verbatim")) {
			t.Fatalf("answer=%q accepted=%v hint=%q err=%v", answer, accepted, hint, err)
		}
	}
}

func TestSupportExcerptUncertainOrUnmatchedUsesAgent(t *testing.T) {
	for _, test := range []struct {
		choice      string
		probability float64
	}{{"agent", .99}, {"s0", .6}, {"unknown", .99}} {
		b := &Bot{Config: &config.Config{OpenRouterDecisionModel: "qwen/qwen3.7-flash"}}
		b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
			return excerptDecision(request, test.choice, test.probability), nil
		})
		m := supportTestMessage(20)
		if answer, ok := b.supportNoteExcerpt(context.Background(), m, []cmd.GarminAIMessage{{Role: "user", Content: "Another question"}}, "note", "Some note"); ok || answer != "" {
			t.Fatalf("uncertain or invalid selection was sent: %q", answer)
		}
	}
}
