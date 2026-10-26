package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func lifecycleMessage(id string) *discord.Message {
	return &discord.Message{ID: snowflakeID(id), GuildID: snowflakePtr("12"), ChannelID: snowflakeID("5"), Author: discord.User{ID: snowflakeID("56")}, Member: &discord.Member{}}
}

func lifecycleClient(t *testing.T, send func() (string, error)) *bot.Client {
	t.Helper()
	client := mustTestClient(t)
	cacheTestGuild(client, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("12")}, Roles: []discord.Role{{ID: snowflakeID("12"), Name: "everyone"}}}})
	client.Caches.AddChannel(testChannel(t, map[string]any{"id": "5", "guild_id": "12"}))
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			var err error
			body, err = send()
			if err != nil {
				return nil, err
			}
		} else if !(r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/reactions/")) && !strings.HasSuffix(r.URL.Path, "/typing") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	return client
}

func lifecycleBot(model cmd.GarminAI) *Bot {
	return &Bot{Logger: zap.NewNop(), garminAI: model, garminAISlots: make(chan struct{}, 2), garminAILastUsed: make(map[string]time.Time)}
}

func TestGarminLifecycleCompactionRejectsMalformedAndEmptySummaries(t *testing.T) {
	for _, content := range []string{"", " ", "unstructured text", `{}`, `null`, `{"summary":" "}`, `{"summary":5}`, `{"summary":"truncated`, `{"shared_memory":["not evidence"]}`, `{"summary":"` + strings.Repeat("word ", 351) + `"}`} {
		t.Run(content[:min(len(content), 40)], func(t *testing.T) {
			messages := []cmd.GarminAIMessage{{Role: "assistant", Name: garminAIConversationSummaryName, Content: "earlier unresolved question"}}
			for i := range garminAICompactionMessages + 1 {
				messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: fmt.Sprint(i)})
			}
			original := copyGarminAIMessages(messages)
			b := lifecycleBot(garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: content}}, nil
			}))
			got := b.prepareGarminAIConversation(context.Background(), nil, lifecycleMessage("100"), messages)
			if !reflect.DeepEqual(got, original) || !reflect.DeepEqual(messages, original) {
				t.Fatal("invalid summary destroyed recent messages or the prior summary")
			}
		})
	}
	if summary := parseGarminAICompaction("```json\n{\"summary\":\"valid summary\",\"shared_memory\":[]}\n```"); summary != "valid summary" {
		t.Fatalf("valid fenced summary rejected: %q", summary)
	}
}

func TestGarminLifecycleCompactionFailureBoundsHonestlyWithoutSplittingExchange(t *testing.T) {
	for _, priorSummary := range []bool{false, true} {
		var messages []cmd.GarminAIMessage
		if priorSummary {
			messages = append(messages, cmd.GarminAIMessage{Role: "assistant", Name: garminAIConversationSummaryName, Content: "prior decisions"})
		}
		for i := range 23 {
			messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: fmt.Sprintf("question %d", i)}, cmd.GarminAIMessage{Role: "assistant", Content: fmt.Sprintf("answer %d", i)})
		}
		messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: "current unresolved request"})
		b := lifecycleBot(garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			return nil, errors.New("offline failure")
		}))
		got := b.prepareGarminAIConversation(context.Background(), nil, lifecycleMessage("100"), messages)
		if len(got) > 2*garminAICompactionMessages+1 || got[0].Name != garminAIConversationSummaryName || !strings.Contains(got[0].Content, "omitted") || got[1].Role != "user" || got[len(got)-1].Content != "current unresolved request" {
			t.Fatalf("incorrect fallback: %#v", got)
		}
		if priorSummary && !strings.Contains(got[0].Content, "prior decisions") {
			t.Fatal("fallback discarded the prior summary")
		}
		if again := boundGarminAIConversation(got); !reflect.DeepEqual(again, got) {
			t.Fatal("bounding is not stable")
		}
	}
}

func TestGarminLifecycleCompactionCancellationAndContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	messages := []cmd.GarminAIMessage{{Role: "assistant", Name: garminAIConversationSummaryName, Content: "earlier summary"}}
	for i := range garminAICompactionMessages + 1 {
		messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: fmt.Sprint(i), Images: []string{"https://example.invalid/image"}, Reasoning: "private reasoning", ReasoningContent: "private reasoning", ReasoningDetails: json.RawMessage(`{"private":true}`)})
	}
	messages[len(messages)-1].Content = "what about the unresolved question?"
	b := lifecycleBot(garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if request.Messages[0].Content != "earlier summary" || !strings.Contains(request.Context, messages[len(messages)-1].Content) {
			t.Error("compaction lost prior summary or current request context")
		}
		for _, m := range request.Messages {
			if len(m.Images) != 0 || m.Reasoning != "" || m.ReasoningContent != "" || len(m.ReasoningDetails) != 0 {
				t.Error("compaction retained image or reasoning data")
			}
		}
		cancel()
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: `{"summary":"late summary","shared_memory":[]}`}}, nil
	}))
	got, err := b.compactGarminAIConversation(ctx, messages, "")
	if !errors.Is(err, context.Canceled) || got != nil || messages[1].Reasoning == "" {
		t.Fatalf("late compaction accepted or input mutated: %#v, %v", got, err)
	}
}

func TestGarminLifecycleSnapshotsDoNotAliasMutableFields(t *testing.T) {
	messages := []cmd.GarminAIMessage{{Role: "user", Images: []string{"original image"}, ReasoningDetails: json.RawMessage(`{"x":1}`), ToolCalls: []cmd.GarminAIToolCall{{ID: "original tool"}}}}
	copied := copyGarminAIMessages(messages)
	copied[0].Images[0] = "changed image"
	copied[0].ReasoningDetails[2] = 'y'
	copied[0].ToolCalls[0].ID = "changed tool"
	if messages[0].Images[0] != "original image" || string(messages[0].ReasoningDetails) != `{"x":1}` || messages[0].ToolCalls[0].ID != "original tool" {
		t.Fatal("snapshot aliases input")
	}
	b := &Bot{}
	m := lifecycleMessage("100")
	messages[0].Reasoning = "do not retain"
	b.rememberGarminAIContext("150", m, messages)
	first, _ := b.garminAIHistory(m, "150", false)
	first[0].Images[0] = "modified snapshot"
	second, _ := b.garminAIHistory(m, "150", false)
	if second[0].Images[0] != "original image" || second[0].Reasoning != "" || len(second[0].ReasoningDetails) != 0 {
		t.Fatal("retained history aliases a snapshot or kept reasoning")
	}
}

func TestGarminLifecycleReferencedImagesStayInChannelAndGuild(t *testing.T) {
	for _, scope := range []string{"channel", "guild", "reference channel"} {
		b := &Bot{garminAI: &fakeGarminAI{}}
		m := lifecycleMessage("100")
		m.ReferencedMessage = &discord.Message{ID: snowflakeID("90"), ChannelID: m.ChannelID, GuildID: m.GuildID, Attachments: []discord.Attachment{{Filename: "image.png", URL: "https://example.invalid/image.png"}}}
		switch scope {
		case "channel":
			m.ReferencedMessage.ChannelID = snowflakeID("6")
		case "guild":
			m.ReferencedMessage.GuildID = snowflakePtr("13")
		case "reference channel":
			m.MessageReference = &discord.MessageReference{MessageID: snowflakePtr("90"), ChannelID: snowflakePtr("6")}
		}
		b.rememberGarminAIContext("90", lifecycleMessage("80"), []cmd.GarminAIMessage{{Role: "user", Content: "same-channel context"}})
		if got := b.garminAIUserMessage(m, "question"); len(got.Images) != 0 {
			t.Fatalf("cross-%s reference exposed images", scope)
		}
		if _, ok := b.garminAIContinuation(m, "continue"); ok {
			t.Fatalf("cross-%s reference resumed a conversation", scope)
		}
	}
}

func TestGarminLifecycleLatestRequestWinsNotLatestCompletion(t *testing.T) {
	b := &Bot{}
	older, newer := lifecycleMessage("100"), lifecycleMessage("200")
	var wg sync.WaitGroup
	newerStored := make(chan struct{})
	wg.Go(func() {
		b.rememberGarminAIContext("250", newer, []cmd.GarminAIMessage{{Role: "user", Content: "newer request"}})
		close(newerStored)
	})
	wg.Go(func() {
		<-newerStored
		b.rememberGarminAIContext("300", older, []cmd.GarminAIMessage{{Role: "user", Content: "older request completed late"}})
	})
	wg.Wait()
	got, ok := b.garminAIHistory(newer, "", false)
	if !ok || got[0].Content != "newer request" {
		t.Fatalf("late completion replaced newer user continuation: %#v", got)
	}
	got, ok = b.garminAIHistory(older, "300", false)
	if !ok || got[0].Content != "older request completed late" {
		t.Fatal("out-of-order reply lost its own linked conversation")
	}
}

func TestGarminLifecycleDuplicateRequestCannotReplaceCancellation(t *testing.T) {
	b := &Bot{}
	m := lifecycleMessage("100")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !b.registerGarminAIRequest(m, cancel) || b.registerGarminAIRequest(m, func() {}) || ctx.Err() != nil {
		t.Fatal("duplicate request replaced or cancelled the original")
	}
	b.cancelDeletedGarminMessage("5", "100")
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("original cancellation was lost")
	}
	b.unregisterGarminAIRequest(m)
	if len(b.garminAIRequests) != 0 || len(b.garminAIParents) != 0 {
		t.Fatal("request bookkeeping leaked")
	}
}

func TestGarminLifecycleDuplicateEventDoesNotSendBusyReply(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	b := lifecycleBot(garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		close(entered)
		<-release
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "hello"}}, nil
	}))
	b.garminAISlots = make(chan struct{}, 1)
	sends := make(chan struct{}, 2)
	client := lifecycleClient(t, func() (string, error) {
		sends <- struct{}{}
		return `{"id":"150","channel_id":"5"}`, nil
	})
	m := lifecycleMessage("100")
	messages := []cmd.GarminAIMessage{{Role: "user", Content: "hello"}}
	go func() {
		defer close(done)
		b.handleGarminAI(client, m, messages)
	}()
	<-entered
	b.handleGarminAI(client, m, messages)
	close(release)
	<-done
	if len(sends) != 1 {
		t.Fatalf("duplicate event sent %d replies", len(sends))
	}
}

func TestGarminLifecycleResetAndDeletionCannotRestoreHistory(t *testing.T) {
	for _, reset := range []bool{false, true} {
		b := &Bot{}
		m := lifecycleMessage("100")
		history := []cmd.GarminAIMessage{{Role: "user", Content: "forget this"}}
		b.rememberGarminAIContext("150", m, history)
		if reset {
			if err := b.resetGarminContext("5", "200"); err != nil {
				t.Fatal(err)
			}
		} else {
			b.cancelDeletedGarminMessage("5", "100")
			b.forgetDeletedGarminContexts("5", "100")
		}
		b.rememberGarminAIUserContext(m, history)
		b.rememberGarminAIContext("250", m, history)
		if _, ok := b.garminAIHistory(m, "", false); ok || len(b.garminAIContexts) != 0 || len(b.garminAIUserContexts) != 0 {
			t.Fatal("late write resurrected forgotten context")
		}
	}
}

func TestGarminLifecycleDeletingParentCancelsDependentRequestOnly(t *testing.T) {
	for _, deletedID := range []string{"100", "150"} {
		b, _ := nativeLogFixture(t)
		original := lifecycleMessage("100")
		b.rememberGarminAIContext("150", original, []cmd.GarminAIMessage{{Role: "user", Content: "parent"}})
		followup := lifecycleMessage("200")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if !b.registerGarminAIRequest(followup, cancel) {
			t.Fatal("registration failed")
		}
		other := lifecycleMessage("300")
		other.Author.ID = snowflakeID("57")
		b.rememberGarminAIContext("350", other, []cmd.GarminAIMessage{{Role: "user", Content: "unrelated"}})
		if deletedID == "100" {
			b.Client.EventManager.HandleGatewayEvent(fixtureGateway{}, gateway.EventTypeMessageDelete, 1, gateway.EventMessageDelete{
				ID: snowflakeID(deletedID), ChannelID: original.ChannelID, GuildID: original.GuildID,
			})
		} else {
			b.Client.EventManager.HandleGatewayEvent(fixtureGateway{}, gateway.EventTypeMessageDeleteBulk, 1, gateway.EventMessageDeleteBulk{
				IDs: []snowflake.ID{snowflakeID(deletedID)}, ChannelID: original.ChannelID, GuildID: original.GuildID,
			})
		}
		b.rememberGarminAIUserContext(followup, []cmd.GarminAIMessage{{Role: "user", Content: "late derived answer"}})
		if ctx.Err() != context.Canceled || b.garminMessageVisible("5", "200") {
			t.Fatal("dependent request survived parent deletion")
		}
		if _, ok := b.garminAIHistory(original, "", false); ok {
			t.Fatal("deleted parent or its in-flight descendant restored user context")
		}
		if _, ok := b.garminAIHistory(other, "350", false); !ok {
			t.Fatal("deletion discarded an unrelated conversation")
		}
		b.unregisterGarminAIRequest(followup)
	}
}

func TestGarminLifecycleFailedSendDoesNotRememberAndSuccessfulSendDeduplicates(t *testing.T) {
	b := &Bot{Logger: zap.NewNop()}
	m := lifecycleMessage("100")
	conversation := []cmd.GarminAIMessage{{Role: "user", Content: "question"}, {Role: "assistant", Content: "answer"}}
	client := lifecycleClient(t, func() (string, error) { return "", errors.New("offline send failure") })
	if b.sendGarminReplyAndRememberIfVisible(client, m, "answer", conversation) != nil || len(b.garminAIContexts) != 0 || len(b.garminAIUserContexts) != 0 {
		t.Fatal("failed send was remembered")
	}
	if !b.registerGarminAIRequest(m, func() {}) {
		t.Fatal("failed send cannot retry")
	}
	b.unregisterGarminAIRequest(m)
	client = lifecycleClient(t, func() (string, error) { return `{"id":"150","channel_id":"5"}`, nil })
	if b.sendGarminReplyAndRememberIfVisible(client, m, "answer", conversation) == nil {
		t.Fatal("successful send failed")
	}
	if b.registerGarminAIRequest(m, func() {}) || len(b.garminAIRequests) != 0 {
		t.Fatal("successful delivery was not deduplicated or leaked request state")
	}
}

func TestGarminLifecycleResetCannotMoveCutoffBackOrEraseNewContext(t *testing.T) {
	b := &Bot{}
	if err := b.resetGarminContext("5", "200"); err != nil {
		t.Fatal(err)
	}
	m := lifecycleMessage("300")
	b.rememberGarminAIContext("350", m, []cmd.GarminAIMessage{{Role: "user", Content: "new context"}})
	for _, cutoff := range []string{"100", "200"} {
		if err := b.resetGarminContext("5", cutoff); err != nil {
			t.Fatal(err)
		}
		if b.garminMessageVisible("5", "150") {
			t.Fatal("stale reset moved cutoff backwards")
		}
		if _, ok := b.garminAIHistory(m, "350", false); !ok {
			t.Fatal("stale or duplicate reset erased new context")
		}
	}
	if err := b.resetGarminContext("5", "invalid"); err == nil {
		t.Fatal("invalid cutoff was accepted")
	}
}

func TestGarminLifecycleResetCancelsInflightWithoutReplyOrRetryRace(t *testing.T) {
	for _, failed := range []bool{false, true} {
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		b := lifecycleBot(garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			close(entered)
			<-release
			if failed {
				return nil, errors.New("late provider error")
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "late answer"}}, nil
		}))
		client := lifecycleClient(t, func() (string, error) {
			t.Error("cancelled request sent a response")
			return `{"id":"150","channel_id":"5"}`, nil
		})
		m := lifecycleMessage("100")
		go func() {
			defer close(done)
			b.handleGarminAI(client, m, []cmd.GarminAIMessage{{Role: "user", Content: "hello"}})
		}()
		<-entered
		// The triggering message is newer than the cutoff, so cancellation must also be honored.
		if err := b.resetGarminContext("5", "90"); err != nil {
			t.Fatal(err)
		}
		if b.registerGarminAIRequest(m, func() {}) {
			t.Fatal("reset allowed a retry to replace a cancelled request before cleanup")
		}
		close(release)
		<-done
		if len(b.garminAIContexts) != 0 || len(b.garminAIUserContexts) != 0 || len(b.garminAIRequests) != 0 || len(b.garminAIParents) != 0 {
			t.Fatal("cancelled request retained context or leaked bookkeeping")
		}
	}
}

func TestGarminLifecycleHistoryCannotFlowBackwardsInTime(t *testing.T) {
	b := &Bot{}
	newer := lifecycleMessage("200")
	b.rememberGarminAIContext("250", newer, []cmd.GarminAIMessage{{Role: "user", Content: "future context"}})
	older := lifecycleMessage("100")
	for _, reference := range []string{"", "250"} {
		if _, ok := b.garminAIHistory(older, reference, false); ok {
			t.Fatal("delayed older event inherited future conversation")
		}
	}
	if _, ok := b.garminAIHistory(newer, "250", false); !ok {
		t.Fatal("chronology rejection destroyed the newer context")
	}
}

func TestGarminLifecycleSilentInteractionStoresOnlyOriginalMessages(t *testing.T) {
	for _, interacted := range []bool{false, true} {
		client := lifecycleClient(t, func() (string, error) {
			t.Error("silent answer sent text")
			return "", nil
		})
		b := lifecycleBot(garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
			if interacted {
				return garminHistoryTestCall("react_to_message", `{"emojis":["\ud83d\udc4d"]}`), nil
			}
			return garminHistoryTestCall("do_not_respond", `{}`), nil
		}))
		m := lifecycleMessage("100")
		messages := []cmd.GarminAIMessage{{Role: "user", Content: "react to this"}}
		b.handleGarminAI(client, m, messages)
		got, ok := b.garminAIHistory(m, "", false)
		if ok != interacted || interacted && !reflect.DeepEqual(got, messages) {
			t.Fatalf("silent retention = %#v, %v; interacted=%v", got, ok, interacted)
		}
	}
}
