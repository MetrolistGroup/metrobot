package discord

import (
	"context"
	"testing"
	"time"

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

func TestRejectedSupportNoticeDeliveryCanRetry(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		if _, routing := request.Questions["route"]; routing {
			return supportTestDecision(request, "agent", .99, .99), nil
		}
		return supportTestDecision(request, "agent", .01, .01), nil
	})
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
	})
	m := supportTestMessage(20)
	messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
	sent.fail.Store(true)
	b.handleGarminAutomaticAppSupport(client, m, messages)
	if len(b.supportSeen) != 0 || b.garminAIUserContexts[garminAIUserContextKey(m)].replyID != "" {
		t.Fatal("failed status delivery prevented a retry")
	}
	sent.fail.Store(false)
	b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
	b.handleGarminAutomaticAppSupport(client, m, messages)
	if len(b.supportSeen) != 1 || b.garminAIUserContexts[garminAIUserContextKey(m)].replyID == "" {
		t.Fatal("retry did not deliver the failure notice")
	}
}

func TestFailureNoticeRespectsDeletedPendingSource(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	original := supportTestMessage(20)
	messages := b.rememberGarminAIUnansweredContext(original, []cmd.GarminAIMessage{{Role: "user", Content: "Original question"}})
	parent := supportTestMessage(21)
	messages = b.rememberGarminAIUnansweredContext(parent, append(messages, cmd.GarminAIMessage{Role: "user", Content: "answer"}))
	current := supportTestMessage(22)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !b.registerGarminAIRequest(current, cancel) {
		t.Fatal("could not register descendant request")
	}
	b.cancelDeletedGarminMessage(original.ChannelID.String(), original.ID.String())
	b.forgetDeletedGarminContexts(original.ChannelID.String(), original.ID.String())
	if b.sendGarminUnansweredReplyIfVisible(client, current, supportUnavailableReply, messages) != nil || sent.count() != 0 {
		t.Fatal("failure notice was sent after its pending source was deleted")
	}
}
