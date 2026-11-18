package discord

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
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

func TestSupportStaysOutOnceHumansTakeOver(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	client.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: 900}})
	var calls atomic.Int32
	b.garminDecisions = supportDecisionTestFunc(func(_ context.Context, request cmd.DecisionRequest) (*cmd.DecisionResponse, error) {
		calls.Add(1)
		return supportTestDecision(request, "agent", .99, .99), nil
	})
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls.Add(1)
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "which Metrolist version are you on?"}}, nil
	})
	asker, staff := snowflake.ID(42), snowflake.ID(43)
	send := func(id int, author snowflake.ID, content string, replyTo snowflake.ID) {
		t.Helper()
		m := supportTestMessage(id)
		m.Author.ID, m.Content = author, content
		if replyTo != 0 {
			m.ReferencedMessage = &discord.Message{ID: snowflakeID("19"), ChannelID: m.ChannelID, Author: discord.User{ID: replyTo}}
		}
		b.garminAILastUsed[author.String()] = time.Now().Add(-garminAICooldown)
		b.onMessageCreate(client, m)
		time.Sleep(50 * time.Millisecond)
	}

	b.refreshSupportSession(supportTestMessage(20)) // asker is mid-session with the bot
	send(21, staff, "what version?", asker)
	send(22, asker, "so you know how to fix this??", 0)
	send(23, staff, "not you!", 900)
	send(24, staff, "aah", 0)
	if calls.Load() != 0 || sent.count() != 0 {
		t.Fatalf("bot joined a staff conversation: calls=%d sent=%d", calls.Load(), sent.count())
	}

	b.supportMu.Lock()
	b.supportHandoffs = nil
	b.supportMu.Unlock()
	send(25, asker, "bro stop spamming just go to sleep", 900)
	if calls.Load() != 0 || sent.count() != 0 {
		t.Fatal("stop request got a reply")
	}
}

func TestUnaddressedSupportFollowupSkipsFallback(t *testing.T) {
	b, client, sent := newSupportDecisionTestBot(t)
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return nil, errors.New("unavailable")
	})
	m := supportTestMessage(20)
	m.Content = "okiw"
	b.handleGarminSupportFollowup(client, m, []cmd.GarminAIMessage{{Role: "user", Content: m.Content}})
	if sent.count() != 0 {
		t.Fatal("unaddressed follow-up got the fallback")
	}
}
