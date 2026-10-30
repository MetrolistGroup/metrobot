package discord

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
)

func TestSupportRetryReservesReferenceAndAnswerRounds(t *testing.T) {
	calls, checks := 0, 0
	b := &Bot{}
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if calls <= 3 {
			return garminHistoryTestCall("load_skill", `{"name":"support"}`), nil
		}
		if calls == 4 {
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
		}
		if calls == 5 {
			if !garminToolAvailable(request.Tools, "load_support_guide") {
				t.Fatal("last-round rejection did not restore source retrieval")
			}
			return garminHistoryTestCall("load_support_guide", `{"name":"discovery"}`), nil
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Your daily discover is on Home in standalone Android."}}, nil
	})
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		checks++
		p := .01
		if checks == 2 {
			p = .99
		}
		return supportTestDecision(request, "agent", p, p), nil
	})
	m := supportTestMessage(20)
	result, err := b.runGarminAI(context.Background(), nil, m, []cmd.GarminAIMessage{{Role: "user", Content: "Is there a discover playlist?"}})
	if err != nil || result.Silent || result.Unverified || calls != 6 || checks != 2 || result.ToolCalls != 4 {
		t.Fatalf("result=%+v err=%v calls=%d checks=%d", result, err, calls, checks)
	}
}

func TestSupportLookupFailureStaysSilentAndRetainsQuestion(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return nil, errors.New("upstream unavailable")
	})
	m := supportTestMessage(20)
	m.Content = "Is there a discover playlist?"
	messages := []cmd.GarminAIMessage{{Role: "user", Content: m.Content}}
	b.handleGarminAutomaticAppSupport(client, m, messages)
	retained := b.garminAIUserContexts[garminAIUserContextKey(m)]
	if sent.count() != 0 || len(retained.messages) != 1 || retained.messages[0].Content != m.Content || !retained.unanswered || !retained.ambientUntil.IsZero() {
		t.Fatal("lookup failure sent a reply or lost the pending question")
	}
	b.handleGarminAutomaticAppSupport(client, m, messages)
	if sent.count() != 0 {
		t.Fatal("duplicate event sent an outage notice")
	}
	followup := supportTestMessage(21)
	followup.Content = "answer"
	messages = b.garminAITriggeredConversation(followup, followup.Content)
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "Your daily discover is on Home in standalone Android."}}, nil
	})
	b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
	b.handleGarminAutomaticAppSupport(client, followup, messages)
	if sent.count() != 1 {
		t.Fatal("the next message could not recover from the lookup failure")
	}
}

func TestPostRejectionCannotChooseSilence(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	calls := 0
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		if _, routing := request.Questions["route"]; routing {
			return supportTestDecision(request, "agent", .99, .99), nil
		}
		return supportTestDecision(request, "agent", .01, .01), nil
	})
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if calls == 1 {
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "UNSUPPORTED_DRAFT"}}, nil
		}
		if garminToolAvailable(request.Tools, "do_not_respond") {
			t.Fatal("rejected support draft could abandon the request instead of correcting it")
		}
		return garminHistoryTestCall("do_not_respond", `{}`), nil
	})
	m := supportTestMessage(20)
	b.handleGarminAutomaticAppSupport(client, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if calls != garminSupportMaxToolRounds+1 || sent.count() != 0 || len(b.supportSeen) != 1 || !b.garminAIUserContexts[garminAIUserContextKey(m)].unanswered {
		t.Fatal("post-rejection silence sent a reply or lost the pending question")
	}
}

func TestIntentionalSupportSilenceClearsPendingQuestion(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		b, client, sent := newSupportDecisionTestBot(t)
		original := supportTestMessage(20)
		messages := b.rememberGarminAIUnansweredContext(original, []cmd.GarminAIMessage{{Role: "user", Content: "Is there a discover playlist?"}})
		withdrawal := supportTestMessage(21)
		withdrawal.Content = "garmin, never mind, thanks"
		messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: "never mind, thanks"})
		b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			if !garminToolAvailable(request.Tools, "do_not_respond") {
				t.Fatal("explicit withdrawal could not choose silence")
			}
			return garminHistoryTestCall("do_not_respond", `{}`), nil
		})
		if automatic {
			b.runSupportDecisionAgent(client, withdrawal, messages, "")
		} else {
			b.handleGarminAI(client, withdrawal, messages)
		}
		later := supportTestMessage(22)
		if sent.count() != 0 || len(b.garminAITriggeredConversation(later, "answer")) != 1 {
			t.Fatal("intentional silence left a false pending question")
		}
	}
}

func TestExplicitNonHelpOutageStaysSilent(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return nil, errors.New("upstream unavailable")
	})
	m := supportTestMessage(20)
	m.Content = "garmin, thanks"
	b.handleGarminAI(client, m, []cmd.GarminAIMessage{{Role: "user", Content: "thanks"}})
	retained := b.garminAIUserContexts[garminAIUserContextKey(m)].messages
	if sent.count() != 0 || len(retained) != 1 {
		t.Fatal("explicit outage sent a reply or lost the question")
	}
}

func TestPendingQuestionDeletionFollowsReferencedReply(t *testing.T) {
	b, _, _ := newSupportDecisionTestBot(t)
	original := supportTestMessage(20)
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "Original question"}}
	messages = b.rememberGarminAIUnansweredContext(original, messages)
	b.rememberGarminAIContext("1000", original, messages)
	followup := supportTestMessage(21)
	followup.Author.ID = 43
	followup.MessageReference = &discord.MessageReference{MessageID: snowflakePtr("1000")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !b.registerGarminAIRequest(followup, cancel) {
		t.Fatal("could not register referenced follow-up")
	}
	b.rememberGarminAIContext("1001", followup, messages)
	b.forgetDeletedGarminContexts(original.ChannelID.String(), original.ID.String())
	if ctx.Err() == nil || len(b.garminAIUserContexts) != 0 || len(b.garminAIContexts) != 0 {
		t.Fatal("deleting a pending source left referenced context or an active derived request")
	}
}

func TestLateReplyDoesNotInheritNewerQuestionSources(t *testing.T) {
	b := &Bot{}
	older := supportTestMessage(20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !b.registerGarminAIRequest(older, cancel) {
		t.Fatal("could not register the older question")
	}
	messages := b.rememberGarminAIUnansweredContext(older, []cmd.GarminAIMessage{{Role: "user", Content: "Older question"}})
	newer := supportTestMessage(21)
	b.rememberGarminAIUnansweredContext(newer, append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "user", Content: "Newer question"}))
	b.rememberGarminAIContext("1000", older, messages)
	b.forgetDeletedGarminContexts(newer.ChannelID.String(), newer.ID.String())
	retained, exists := b.garminAIContexts["1000"]
	if !exists || ctx.Err() != nil || len(retained.pendingSources) != 1 || retained.pendingSources[0] != older.ID.String() {
		t.Fatal("deleting the newer question erased an independent older reply")
	}
	b.forgetDeletedGarminContexts(older.ChannelID.String(), older.ID.String())
	if _, exists := b.garminAIContexts["1000"]; exists {
		t.Fatal("the older reply lost its own deletion source")
	}
}

func TestPendingQuestionLineageIsBounded(t *testing.T) {
	b := &Bot{}
	var messages []cmd.GarminAIMessage
	for i := 0; i <= 2*garminAICompactionMessages; i++ {
		m := supportTestMessage(20 + i)
		messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: "question"})
		messages = b.rememberGarminAIUnansweredContext(m, messages)
	}
	m := supportTestMessage(20 + 2*garminAICompactionMessages)
	retained := b.garminAIUserContexts[garminAIUserContextKey(m)]
	if len(retained.pendingSources) != 1 || len(retained.messages) != 1 || len(messages) != 1 {
		t.Fatal("lineage overflow kept history without its deletion sources")
	}
}
