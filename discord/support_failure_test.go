package discord

import (
	"context"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestSupportRewriteCanReuseAlreadyRetrievedNote(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	if err := b.DB.AddNote("up", "Update standalone Android", "For GitHub Android builds, open Settings → Updater → Check for updates."); err != nil {
		t.Fatal(err)
	}
	calls, checks := 0, 0
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		answer := "UNSUPPORTED_DRAFT"
		if calls > 1 {
			answer = "For GitHub Android builds, open Settings → Updater → Check for updates."
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: answer}}, nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		checks++
		p := .01
		if checks > 1 {
			p = .99
		}
		return supportTestDecision(request, "agent", p, p), nil
	})
	ctx := context.WithValue(context.Background(), automaticSupportContextKey{}, true)
	ctx = context.WithValue(ctx, supportSelectedNoteContextKey{}, "up")
	m := supportTestMessage(20)
	result, err := b.runGarminAI(ctx, nil, m, []cmd.GarminAIMessage{{Role: "user", Content: "How do I update standalone Android from GitHub?"}})
	if err != nil || result.Silent || result.Unverified || calls != 2 || checks != 2 || result.ToolCalls != 1 {
		t.Fatalf("already-retrieved evidence was needlessly discarded: result=%+v err=%v calls=%d checks=%d", result, err, calls, checks)
	}
}
