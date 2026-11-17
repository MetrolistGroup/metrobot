package discord

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/snowflake/v2"
)

func TestSupportSessionSkipsRouterUntilResolvedOrIdle(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	var routed, chats atomic.Int32
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		if _, routing := request.Questions["route"]; routing {
			routed.Add(1)
			return supportTestDecision(request, "none", 0, 0), nil
		}
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		chats.Add(1)
		if strings.Contains(request.Messages[len(request.Messages)-1].Content, "that worked") {
			return garminHistoryTestCall("do_not_respond", `{}`), nil
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "which Metrolist version are you on?"}}, nil
	})
	send := func(id int, author snowflake.ID, content string) {
		t.Helper()
		m := supportTestMessage(id)
		m.Author.ID = author
		m.Content = content
		before := chats.Load() + routed.Load()
		b.garminAILastUsed[m.Author.ID.String()] = time.Now().Add(-garminAICooldown)
		b.onMessageCreate(client, m)
		for deadline := time.Now().Add(2 * time.Second); chats.Load()+routed.Load() == before && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond) // let the handler finish its delivery
	}
	asker, other := snowflake.ID(42), snowflake.ID(43)

	send(20, asker, "garmin, playback keeps stopping")
	if sent.count() != 1 || chats.Load() != 1 {
		t.Fatalf("explicit question not answered: sent=%d chats=%d", sent.count(), chats.Load())
	}
	send(21, asker, "android 15, latest stable")
	if routed.Load() != 0 || chats.Load() != 2 || sent.count() != 2 {
		t.Fatalf("session follow-up used the router: routed=%d chats=%d sent=%d", routed.Load(), chats.Load(), sent.count())
	}
	send(22, other, "same problem here")
	if routed.Load() != 1 {
		t.Fatal("another user's message skipped the router")
	}
	send(23, asker, "that worked, thanks")
	if sent.count() != 2 {
		t.Fatal("resolution message got a reply")
	}
	send(24, asker, "cool")
	if routed.Load() != 2 {
		t.Fatal("session survived the resolution")
	}

	// An idle session expires.
	b.refreshSupportSession(supportTestMessage(25))
	b.supportMu.Lock()
	for key := range b.supportSessions {
		b.supportSessions[key] = time.Now().Add(-time.Second)
	}
	b.supportMu.Unlock()
	send(26, asker, "one more thing")
	if routed.Load() != 3 {
		t.Fatal("expired session skipped the router")
	}
}
