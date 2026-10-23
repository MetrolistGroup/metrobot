package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestExtractGarminPromptFromBotMention(t *testing.T) {
	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{
		ID: snowflakeID("123")}})

	for _, content := range []string{"<@123> hello", "<@!123>, hello", "hey <@123>"} {
		if prompt, triggered := extractGarminPrompt(session, content); !triggered || !strings.Contains(prompt, "hello") && !strings.Contains(prompt, "hey") {
			t.Errorf("extractGarminPrompt(%q) = (%q, %v)", content, prompt, triggered)
		}
	}
}

func TestTruncateGarminAIResponse(t *testing.T) {
	input := strings.Repeat("a", garminAIMaxContent+1)
	got := truncateGarminAIResponse(input)
	if len(got) > garminAIMaxContent {
		t.Fatalf("response length = %d, max %d", len(got), garminAIMaxContent)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated response = %q, want ellipsis", got)
	}
}

func TestTruncateGarminAIResponsePreservesUTF8(t *testing.T) {
	input := strings.Repeat("é", garminAIMaxContent)
	got := truncateGarminAIResponse(input)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated response is invalid UTF-8: %q", got)
	}
}

func TestGarminAIContinuationIncludesBoundedContext(t *testing.T) {
	bot := &Bot{
		garminAI:             &fakeGarminAI{},
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	history := []cmd.GarminAIMessage{{Role: "user", Content: "one"}, {Role: "assistant", Content: "one answer"}, {Role: "user", Content: "two"}, {Role: "assistant", Content: "two answer"}, {Role: "user", Content: "three"}, {Role: "assistant", Content: "three answer"}}
	original := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000056")},
	}
	bot.rememberGarminAIContext("100000000000000003", original, history)

	messages, ok := bot.garminAIContinuation(&discord.Message{
		GuildID:   snowflakePtr("100000000000000012"),
		ChannelID: snowflakeID("100000000000000005"), MessageReference: &discord.MessageReference{MessageID: snowflakePtr("100000000000000003")},
		Author: discord.User{ID: snowflakeID("100000000000000056")},
	}, "  follow up  ")
	if !ok {
		t.Fatal("garminAIContinuation() did not recognize tracked reply")
	}
	want := []cmd.GarminAIMessage{{Role: "user", Content: "one"}, {Role: "assistant", Content: "one answer"}, {Role: "user", Content: "two"}, {Role: "assistant", Content: "two answer"}, {Role: "user", Content: "three"}, {Role: "assistant", Content: "three answer"}, {Role: "user", Name: "discord_100000000000000056", Content: "follow up"}}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("messages = %#v, want %#v", messages, want)
	}
}

func TestGarminAIConversationCompactsEveryTwentyMessages(t *testing.T) {
	model := &summaryGarminAI{summary: "Nyx (discord_100000000000000027) chose option B; the game score is 4-2."}
	bot := &Bot{garminAI: model}
	messages := make([]cmd.GarminAIMessage, 0, garminAICompactionMessages+1)
	for index := range garminAICompactionMessages {
		role := "assistant"
		name := ""
		if index%2 == 0 {
			role = "user"
			name = "discord_100000000000000027"
			if index == 2 {
				name = "discord_lamp"
			}
		}
		messages = append(messages, cmd.GarminAIMessage{Role: role, Name: name, Content: fmt.Sprintf("message %d", index), Images: []string{"https://example.com/image.png"}, Timestamp: time.Date(2026, 6, 20, 12, index, 0, 0, time.UTC)})
	}
	messages = append(messages, cmd.GarminAIMessage{Role: "user", Name: "discord_100000000000000027", Content: "continue"})

	got, err := bot.compactGarminAIConversation(context.Background(), messages, `{"tracked_conversation_users":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	recent := garminAICompactionMessages / 2
	if len(got) != recent+1 || got[0].Name != garminAIConversationSummaryName || !reflect.DeepEqual(got[1:], messages[len(messages)-recent:]) || !strings.Contains(got[0].Content, model.summary) || !strings.Contains(got[0].Content, "discord_lamp") {
		t.Fatalf("compacted conversation = %#v", got)
	}
	if got[0].Timestamp != messages[len(messages)-recent-1].Timestamp || !strings.Contains(model.request.Context, "2026-06-20T12:00:00Z") {
		t.Fatalf("compaction lost message times: %#v, context %s", got[0], model.request.Context)
	}
	if len(model.request.Messages) != len(messages)-recent || !model.request.DisableReasoning || model.request.Context == "" {
		t.Fatalf("summary request = %#v", model.request)
	}
	for _, message := range model.request.Messages {
		if len(message.Images) != 0 {
			t.Fatalf("summary request retained image data: %#v", message)
		}
	}

	model.summary = "merged summary"
	secondCycle := append(copyGarminAIMessages(got[:1]), messages[:garminAICompactionMessages]...)
	secondCycle = append(secondCycle, cmd.GarminAIMessage{Role: "user", Name: "discord_100000000000000027", Content: "next cycle"})
	got, err = bot.compactGarminAIConversation(context.Background(), secondCycle, "context")
	if err != nil || len(got) != recent+1 || got[len(got)-1].Content != "next cycle" || len(model.request.Messages) != len(secondCycle)-recent || model.request.Messages[0].Name != garminAIConversationSummaryName {
		t.Fatalf("second compaction = %#v, request %#v, error %v", got, model.request, err)
	}

	model.request = cmd.GarminAIRequest{}
	unchanged, err := bot.compactGarminAIConversation(context.Background(), messages[:garminAICompactionMessages], "")
	if err != nil || len(unchanged) != garminAICompactionMessages || len(model.request.Messages) != 0 {
		t.Fatalf("exact boundary was compacted: %#v, %v", unchanged, err)
	}
}

func TestGarminAIConversationPersistsSharedMemory(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	model := &summaryGarminAI{summary: `{"summary":"the project decision was recorded","shared_memory":["Metrolist release notes use Markdown"]}`}
	bot := &Bot{garminAI: model, garminMemory: memory}
	messages := make([]cmd.GarminAIMessage, garminAICompactionMessages+1)
	for index := range messages {
		messages[index] = cmd.GarminAIMessage{Role: "user", Content: fmt.Sprintf("message %d", index)}
	}
	compacted, err := bot.compactGarminAIConversation(context.Background(), messages, "context")
	if err != nil {
		t.Fatal(err)
	}
	learned, err := memory.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compacted[0].Content, "the project decision was recorded") || !strings.Contains(learned, "Metrolist release notes use Markdown") {
		t.Fatalf("compacted = %#v, memory = %q", compacted, learned)
	}
}

func TestGarminAIContinuationRejectsUntrackedAndExpiredReplies(t *testing.T) {
	bot := &Bot{
		garminAI: &fakeGarminAI{},
		garminAIContexts: map[string]garminAIContext{
			"expired": {userID: "100000000000000056", channelID: "100000000000000005", expiresAt: time.Now().Add(-time.Minute)},
		},
	}
	for _, messageID := range []string{"other-bot-message", "expired"} {
		messages, ok := bot.garminAIContinuation(&discord.Message{
			GuildID:   snowflakePtr("100000000000000012"),
			ChannelID: snowflakeID("100000000000000005"), MessageReference: &discord.MessageReference{MessageID: snowflakePtr(messageID)},
			Author: discord.User{ID: snowflakeID("100000000000000056")},
		}, "follow up")
		if ok || messages != nil {
			t.Fatalf("garminAIContinuation(%q) = (%#v, %v), want rejected", messageID, messages, ok)
		}
	}
}

func TestGarminAIContinuationDoesNotWakeOnHumanReplyWithUserHistory(t *testing.T) {
	bot := &Bot{
		garminAI:             &fakeGarminAI{},
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	original := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000027")},
	}
	bot.rememberGarminAIContext("tracked-bot-reply", original, []cmd.GarminAIMessage{{Role: "user", Content: "garmin, hello"}, {Role: "assistant", Content: "hey nyx"}})
	messages, ok := bot.garminAIContinuation(&discord.Message{
		GuildID:   snowflakePtr("100000000000000012"),
		ChannelID: snowflakeID("100000000000000005"), MessageReference: &discord.MessageReference{MessageID: snowflakePtr("100000000000000015")},
		ReferencedMessage: &discord.Message{ID: snowflakeID("100000000000000015"), Author: discord.User{ID: snowflakeID("100000000000000014")}},
		Author:            discord.User{ID: snowflakeID("100000000000000027")},
	}, "me and lamp")
	if ok || messages != nil {
		t.Fatalf("human reply woke Garmin with messages %#v", messages)
	}
}

func TestGarminAITriggeredConversationUsesActiveHistory(t *testing.T) {
	bot := &Bot{
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	history := []cmd.GarminAIMessage{{Role: "user", Content: "always add a heart"}, {Role: "assistant", Content: "got it ❤️"}}
	message := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000018")},
	}
	bot.rememberGarminAIContext("old-reply", message, history)
	got := bot.garminAITriggeredConversation(message, "you forgot")
	want := append(history, cmd.GarminAIMessage{Role: "user", Name: "discord_100000000000000018", Content: "you forgot"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("triggered conversation = %#v, want %#v", got, want)
	}
}

func TestGarminAISporadicRetentionIsSeparateFromAmbient(t *testing.T) {
	for _, age := range []time.Duration{3 * time.Minute, 6 * time.Hour, 23 * time.Hour, 25 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			b := &Bot{garminAI: &fakeGarminAI{}}
			message := &discord.Message{GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000056")}}
			history := []cmd.GarminAIMessage{{Role: "user", Content: "we chose option B", Timestamp: time.Now().UTC().Add(-age)}}
			b.storeGarminAIContextLocked("150", message, history, time.Now().Add(-age))
			retained := age < 24*time.Hour
			if _, ok := b.garminAIAmbientContinuation(message, "still here?"); ok {
				t.Fatal("sporadic history reopened ambient replies")
			}
			got := b.garminAITriggeredConversation(message, "what did we choose?")
			if (len(got) == 2) != retained || retained && !reflect.DeepEqual(got[:1], history) {
				t.Fatalf("addressed conversation after ambient probe = %#v, retained = %v", got, retained)
			}
			if _, exists := b.garminAIUserContexts[garminAIUserContextKey(message)]; exists != retained {
				t.Fatalf("user history retained = %v, want %v", exists, retained)
			}
			message.MessageReference = &discord.MessageReference{MessageID: snowflakePtr("150")}
			if _, ok := b.garminAIContinuation(message, "continue"); ok != retained {
				t.Fatalf("referenced conversation retained = %v, want %v", ok, retained)
			}
		})
	}
}

func TestGarminAITriggeredConversationPrefersReferencedHistoryAndStaysIsolated(t *testing.T) {
	b := &Bot{garminAI: &fakeGarminAI{}}
	message := &discord.Message{GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000056")}}
	b.rememberGarminAIContext("150", message, []cmd.GarminAIMessage{{Role: "user", Content: "the original topic"}})
	b.rememberGarminAIContext("160", message, []cmd.GarminAIMessage{{Role: "user", Content: "an unrelated newer topic"}})
	for _, reference := range []bool{true, false} {
		reply := *message
		if reference {
			reply.MessageReference = &discord.MessageReference{MessageID: snowflakePtr("150")}
		} else {
			reply.ReferencedMessage = &discord.Message{ID: snowflakeID("150")}
		}
		if got := b.garminAITriggeredConversation(&reply, "continue"); len(got) != 2 || got[0].Content != "the original topic" {
			t.Fatalf("referenced history lost to latest user context: %#v", got)
		}
	}
	for _, scope := range []string{"user", "guild", "channel"} {
		other := *message
		switch scope {
		case "user":
			other.Author.ID = snowflakeID("100000000000000057")
		case "guild":
			other.GuildID = snowflakePtr("100000000000000013")
		case "channel":
			other.ChannelID = snowflakeID("100000000000000006")
		}
		if got := b.garminAITriggeredConversation(&other, "hello"); len(got) != 1 {
			t.Fatalf("cross-%s addressed history exposed: %#v", scope, got)
		}
	}
}

func TestGarminAIStorageLeavesOldestBatchForCompactionAndStaysBounded(t *testing.T) {
	model := &summaryGarminAI{summary: "the first decision was option B"}
	b := &Bot{garminAI: model}
	message := &discord.Message{ChannelID: snowflakeID("5"), Author: discord.User{ID: snowflakeID("56")}}
	history := make([]cmd.GarminAIMessage, garminAICompactionMessages+2)
	for index := range history {
		history[index] = cmd.GarminAIMessage{Role: "user", Content: fmt.Sprintf("detail %d", index)}
	}
	b.rememberGarminAIUserContext(message, history)
	stored, _ := b.garminAIHistory(message, "", false)
	if !reflect.DeepEqual(stored, history) {
		t.Fatal("storage dropped details before compaction could summarize them")
	}
	if _, err := b.compactGarminAIConversation(context.Background(), stored, ""); err != nil || model.request.Messages[0].Content != "detail 0" {
		t.Fatalf("oldest detail missed compaction: %#v, %v", model.request, err)
	}
	now := time.Now()
	for index := range garminAIContextMax + 1 {
		message.Author.ID = snowflakeID(fmt.Sprint(index + 1000))
		b.storeGarminAIContextLocked(fmt.Sprint(index), message, make([]cmd.GarminAIMessage, 100), now.Add(time.Duration(index)*time.Millisecond))
	}
	if len(b.garminAIContexts) != garminAIContextMax || len(b.garminAIUserContexts) != garminAIContextMax {
		t.Fatalf("context limits = %d replies, %d users", len(b.garminAIContexts), len(b.garminAIUserContexts))
	}
	if _, exists := b.garminAIContexts["0"]; exists {
		t.Fatal("oldest context was not evicted")
	}
	for _, context := range b.garminAIContexts {
		if len(context.messages) > 2*garminAICompactionMessages {
			t.Fatalf("stored conversation is unbounded: %d messages", len(context.messages))
		}
	}
}

func TestGarminAIContextAllowsReplyParticipantsButScopesAmbientToUser(t *testing.T) {
	bot := &Bot{
		garminAI:             &fakeGarminAI{},
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	original := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000033")},
	}
	history := []cmd.GarminAIMessage{{Role: "user", Content: "public thread"}, {Role: "assistant", Content: "reply"}}
	bot.rememberGarminAIContext("100000000000000003", original, history)

	otherUser := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000030")}, MessageReference: &discord.MessageReference{MessageID: snowflakePtr("100000000000000003")},
	}
	if messages, ok := bot.garminAIContinuation(otherUser, "show me"); !ok || len(messages) != 3 || messages[2].Content != "show me" {
		t.Fatalf("same-channel participant continuation = (%#v, %v)", messages, ok)
	}

	for _, attempt := range []*discord.Message{{GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000031"), Author: discord.User{ID: snowflakeID("100000000000000033")}, MessageReference: &discord.MessageReference{MessageID: snowflakePtr("100000000000000003")}}, {GuildID: snowflakePtr("100000000000000032"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000033")}, MessageReference: &discord.MessageReference{MessageID: snowflakePtr("100000000000000003")}}} {
		if messages, ok := bot.garminAIContinuation(attempt, "show me"); ok || messages != nil {
			t.Fatalf("cross-channel/guild reply exposed context: %#v", messages)
		}
	}

	sameUser := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000033")}, MessageReference: &discord.MessageReference{MessageID: snowflakePtr("100000000000000003")},
	}
	if _, ok := bot.garminAIContinuation(sameUser, "continue"); !ok {
		t.Fatal("authorized reply context was lost after rejected attempts")
	}

	ambient := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000033")},
	}
	messages, ok := bot.garminAIAmbientContinuation(ambient, "one more thing")
	if !ok || len(messages) != 3 || messages[2].Content != "one more thing" {
		t.Fatalf("ambient continuation = (%#v, %v)", messages, ok)
	}
	for _, attempt := range []*discord.Message{{GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000030")}}, {GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000031"), Author: discord.User{ID: snowflakeID("100000000000000033")}}, {GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000033")}, MessageReference: &discord.MessageReference{MessageID: snowflakePtr("100000000000000014")}}} {
		if messages, ok := bot.garminAIAmbientContinuation(attempt, "one more thing"); ok || messages != nil {
			t.Fatalf("invalid ambient continuation exposed context: %#v", messages)
		}
	}
	if !bot.stopGarminAIAmbient(ambient, "bor shut up") {
		t.Fatal("ambient stop request was ignored")
	}
	if messages, ok := bot.garminAIAmbientContinuation(ambient, "still there?"); ok || messages != nil {
		t.Fatalf("ambient context survived stop request: %#v", messages)
	}
	bot.rememberGarminAIContext("bot-message-2", original, history)

	key := garminAIUserContextKey(original)
	context := bot.garminAIUserContexts[key]
	context.ambientUntil = time.Now().Add(-time.Minute)
	bot.garminAIUserContexts[key] = context
	if messages, ok := bot.garminAIAmbientContinuation(ambient, "too late"); ok || messages != nil {
		t.Fatalf("expired ambient continuation = (%#v, %v)", messages, ok)
	}
	context.ambientUntil = time.Now().Add(time.Minute)
	bot.garminAIUserContexts[key] = context
	ambientToken := bot.tryBeginGarminAIAmbient(ambient)
	if ambientToken == 0 || bot.tryBeginGarminAIAmbient(ambient) != 0 {
		t.Fatal("ambient single-flight gate did not drop overlap")
	}
	bot.endGarminAIAmbient(ambient, ambientToken)
	if bot.tryBeginGarminAIAmbient(ambient) != 0 {
		t.Fatal("ambient cooldown did not drop immediate retry")
	}
}

func TestGarminContextResetHidesPriorMessagesAndClearsChains(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	bot := &Bot{
		DB: database, Logger: zap.NewNop(),
		garminAIContexts: make(map[string]garminAIContext), garminAIUserContexts: make(map[string]garminAIContext),
		garminContextCutoffs: make(map[string]string), garminAIAmbientBusy: make(map[string]uint64),
	}
	original := &discord.Message{ID: snowflakeID("100"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000056")}}
	bot.rememberGarminAIContext("150", original, []cmd.GarminAIMessage{{Role: "user", Content: "old context"}})
	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	if !bot.registerGarminAIRequest(original, cancelRequest) {
		t.Fatal("failed to register pre-reset request")
	}
	if err := bot.resetGarminContext("100000000000000005", "200"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requestContext.Done():
	default:
		t.Fatal("context reset did not cancel in-flight request")
	}
	if len(bot.garminAIContexts) != 0 || len(bot.garminAIUserContexts) != 0 {
		t.Fatal("context reset retained an in-memory chain")
	}
	if bot.garminMessageVisible("100000000000000005", "199") || !bot.garminMessageVisible("100000000000000005", "201") {
		t.Fatal("context cutoff did not hide only prior messages")
	}
	message := &discord.Message{
		ID: snowflakeID("201"), GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID("100000000000000005"), Author: discord.User{ID: snowflakeID("100000000000000056"), Username: "user"},
		ReferencedMessage: &discord.Message{ID: snowflakeID("199"), Content: "old secret", Author: discord.User{ID: snowflakeID("100000000000000030"), Username: "other"}},
	}
	if context := bot.garminDiscordContextForMessage(nil, message); strings.Contains(context, "old secret") {
		t.Fatalf("old replied message survived cutoff: %s", context)
	}
}

func TestGarminAIAmbientDisabledInGeneral(t *testing.T) {
	state := mustTestClient(t)
	cacheTestGuild(state, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}}})

	for _, channel := range []discord.GuildChannel{testChannel(t, map[string]any{"id": garminGeneralID, "guild_id": "100000000000000012"}), testChannel(t, map[string]any{"id": "100000000000000011", "guild_id": "100000000000000012", "parent_id": garminGeneralID, "type": discord.ChannelTypeGuildPublicThread}), testChannel(t, map[string]any{"id": garminChatThreadID, "guild_id": "100000000000000012", "parent_id": garminGeneralID, "type": discord.ChannelTypeGuildPublicThread}), testChannel(t, map[string]any{"id": garminBotsID, "guild_id": "100000000000000012", "type": discord.ChannelTypeGuildText})} {
		state.Caches.AddChannel(channel)

	}
	session := state
	if garminAIAmbientEnabled(session, garminGeneralID) || garminAIAmbientEnabled(session, "100000000000000011") {
		t.Fatal("ambient mode was enabled in general")
	}
	if !garminAIAmbientEnabled(session, garminChatThreadID) || !garminAIAmbientEnabled(session, garminBotsID) {
		t.Fatal("ambient mode was disabled outside general")
	}
}

func TestGarminAIAmbientReplyPolicy(t *testing.T) {
	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{
		ID: snowflakeID("100000000000000002")}})

	message := &discord.Message{Author: discord.User{ID: snowflakeID("100000000000000056")}}
	for content, want := range map[string]bool{
		"waow":                    false,
		"LOVELY":                  false,
		"metrobot is alive again": false,
		"they*":                   false,
		"what do you mean?":       true,
		"can you explain that":    true,
		"metrobot, are you okay?": true,
	} {
		if got := garminAIAmbientTextReplyEligible(session, message, content); got != want {
			t.Errorf("ambient reply eligibility for %q = %v, want %v", content, got, want)
		}
	}
	message.Mentions = []discord.User{{ID: snowflakeID("100000000000000030")}}
	if !garminAIAmbientTargetsOtherUser(session, message) {
		t.Fatal("ambient message addressing another user was accepted")
	}
}

func TestRenderGarminGuildEmojisUsesOnlyLiveAvailableNames(t *testing.T) {
	state := mustTestClient(t)
	cacheTestGuild(state, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}, Emojis: []discord.Emoji{{ID: snowflakeID("1"), Name: "glup", Available: true}, {ID: snowflakeID("2"), Name: "soggy~1", Available: true, Animated: true}, {ID: snowflakeID("3"), Name: "gone", Available: false}}}})

	session := state
	got := renderGarminGuildEmojis(session, "100000000000000012", "i love :glup: and <:soggy~1:999>, not :gone: or :fake:")
	want := "i love <:glup:1> and <a:soggy~1:2>, not or"
	if got != want {
		t.Fatalf("rendered emojis = %q, want %q", got, want)
	}
}

func TestRenderGarminGuildEmojisRepairsMissingClosingColon(t *testing.T) {
	state := mustTestClient(t)
	cacheTestGuild(state, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}, Emojis: []discord.Emoji{{
		ID: snowflakeID("1"), Name: "hm", Available: true,
	}}}})

	session := state
	for input, want := range map[string]string{
		"nah i don't have jokes :hm": "nah i don't have jokes <:hm:1>",
		":hm is aesthetic":           "<:hm:1> is aesthetic",
		"keep :missing as text":      "keep :missing as text",
	} {
		if got := renderGarminGuildEmojis(session, "100000000000000012", input); got != want {
			t.Errorf("renderGarminGuildEmojis(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRenderGarminGuildEmojisRefreshesStaleState(t *testing.T) {
	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}, Emojis: []discord.Emoji{{ID: snowflakeID("100000000000000028"), Name: "glup", Available: true}}}})
	session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `[{"id":"100000000000000025","name":"glup","available":true}]`

		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})})

	if got := renderGarminGuildEmojis(session, "100000000000000012", ":glup:"); got != "<:glup:100000000000000025>" {
		t.Fatalf("refreshed emoji = %q", got)
	}
}

func TestGarminEmojiToolsListAndViewLiveGuildEmojis(t *testing.T) {
	state := mustTestClient(t)
	cacheTestGuild(state, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}, Emojis: []discord.Emoji{{ID: snowflakeID("1"), Name: "glup", Available: true}, {ID: snowflakeID("2"), Name: "soggy~1", Available: true, Animated: true}}}})

	session := state
	bot := &Bot{}
	listed, err := bot.listGarminGuildEmojis(session, "100000000000000012")
	if err != nil || !strings.Contains(listed, `"name":"soggy~1"`) {
		t.Fatalf("listed emojis = %q, error %v", listed, err)
	}
	viewed, err := bot.viewGarminGuildEmoji(session, "100000000000000012", "soggy~1")
	if err != nil || !strings.Contains(viewed, `"image_url":"https://cdn.discordapp.com/emojis/2.gif`) {
		t.Fatalf("viewed emoji = %q, error %v", viewed, err)
	}
	if images := garminAIToolImageURLs("view_discord_emoji", viewed); len(images) != 1 {
		t.Fatalf("viewed emoji images = %v", images)
	}
}

func TestEnforceGarminGeneralReplyIsShortWithoutRedirect(t *testing.T) {
	got := enforceGarminChannelReply(garminGeneralID, "the setting is under Settings > Playback. here's some extra detail.", "where is the setting?")
	want := "the setting is under Settings > Playback."
	if got != want {
		t.Fatalf("enforceGarminChannelReply() = %q, want %q", got, want)
	}
	if strings.Contains(got, "#bots") || strings.Contains(got, garminBotsID) {
		t.Fatalf("general reply redirected to #bots: %q", got)
	}
	if got := enforceGarminChannelReply("another-channel", "yeah, sure. what's on your mind?", "hello"); got != "yeah, sure. what's on your mind?" {
		t.Fatalf("non-general reply changed to %q", got)
	}
}

func TestEnforceGarminGeneralReplyAllowsRequestedDetail(t *testing.T) {
	answer := "we chose option B.\n- Lamp corrected the date.\n- the release is still undecided."
	for _, prompt := range []string{"Summarize this conversation", "give me a recap", "more details please", "explain in detail", "what did we talk about?", "who said what?", "show our conversation history"} {
		if got := enforceGarminChannelReply(garminGeneralID, answer, prompt); got != answer {
			t.Errorf("requested detail %q was truncated: %q", prompt, got)
		}
	}
	if got := enforceGarminChannelReply(garminGeneralID, answer, "hello"); got != "we chose option B." {
		t.Fatalf("ordinary general policy changed: %q", got)
	}
}

func TestEnforceGarminThreadUnderGeneralStaysShort(t *testing.T) {
	state := mustTestClient(t)
	cacheTestGuild(state, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}}})
	state.Caches.AddChannel(testChannel(t, map[string]any{"id": garminGeneralID, "guild_id": "100000000000000012", "name": "general"}))
	state.Caches.AddChannel(testChannel(t, map[string]any{"id": "100000000000000011", "guild_id": "100000000000000012", "parent_id": garminGeneralID, "name": "thread", "type": discord.ChannelTypeGuildPublicThread}))

	session := state
	got := enforceGarminChannelReply(garminRedirectChannelID(session, "100000000000000011"), "the answer is 42. here's some extra detail.", "what is the answer?")
	if got != "the answer is 42." {
		t.Fatalf("thread reply = %q", got)
	}
}

func TestGarminChatThreadUnderGeneralAllowsNormalReplies(t *testing.T) {
	state := mustTestClient(t)
	cacheTestGuild(state, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}}})
	state.Caches.AddChannel(testChannel(t, map[string]any{"id": garminGeneralID, "guild_id": "100000000000000012", "name": "general"}))
	state.Caches.AddChannel(testChannel(t, map[string]any{"id": garminChatThreadID, "guild_id": "100000000000000012", "parent_id": garminGeneralID, "name": "thread", "type": discord.ChannelTypeGuildPublicThread}))

	session := state
	answer := "yeah, sure. what's on your mind?"
	if got := enforceGarminChannelReply(garminRedirectChannelID(session, garminChatThreadID), answer, "hello"); got != answer {
		t.Fatalf("allowed thread reply = %q, want %q", got, answer)
	}
	message := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), ChannelID: snowflakeID(garminChatThreadID), Author: discord.User{ID: snowflakeID("100000000000000056"), Username: "speaker"}, Member: &discord.Member{},
	}
	context := (&Bot{}).garminDiscordContextForMessage(session, message)
	if !strings.Contains(context, "normal conversations with Metrobot are welcome") || strings.Contains(context, "#bots") {
		t.Fatalf("allowed thread received general-only context: %s", context)
	}
}

func TestHandleGarminAIDoNotRespond(t *testing.T) {
	handled, err := (&Bot{}).handleGarminAIMessageAction(nil, nil, cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{
		Name:      "do_not_respond",
		Arguments: `{}`,
	}}, 0)
	if err != nil || !handled {
		t.Fatalf("do_not_respond = (%v, %v), want handled", handled, err)
	}
}

func TestGarminDirectSlurDetection(t *testing.T) {
	for _, prompt := range []string{
		"you are a nigger",
		"f.a.g.g.o.t",
		"stupid r3tard",
	} {
		if !garminDirectSlur(prompt) {
			t.Errorf("garminDirectSlur(%q) = false, want true", prompt)
		}
	}
	for _, prompt := range []string{
		"what does the n-word mean?",
		"someone called me a faggot",
		"why is nigger a slur?",
		"fuck you",
		"hello",
	} {
		if garminDirectSlur(prompt) {
			t.Errorf("garminDirectSlur(%q) = true, want false", prompt)
		}
	}
}

func TestGarminAIUserMessageIncludesImageAttachments(t *testing.T) {
	message := &discord.Message{
		ChannelID:         snowflakeID("100000000000000005"),
		Author:            discord.User{ID: snowflakeID("123456789012345678")},
		Attachments:       []discord.Attachment{{Filename: "photo.png", ContentType: ptr("image/png"), URL: "https://cdn.discordapp.com/attachments/photo.png"}, {Filename: "notes.txt", ContentType: ptr("text/plain"), URL: "https://cdn.discordapp.com/attachments/notes.txt"}, {Filename: "fallback.webp", URL: "https://media.discordapp.net/attachments/fallback.webp"}},
		ReferencedMessage: &discord.Message{ID: snowflakeID("200"), Attachments: []discord.Attachment{{Filename: "replied.jpg", ContentType: ptr("image/jpeg"), URL: "https://cdn.discordapp.com/attachments/replied.jpg"}}},
	}
	bot := &Bot{}
	got := bot.garminAIUserMessage(message, "  what is this?  ")
	want := cmd.GarminAIMessage{
		Role:    "user",
		Name:    "discord_123456789012345678",
		Content: "what is this?",
		Images: []string{
			"https://cdn.discordapp.com/attachments/photo.png",
			"https://media.discordapp.net/attachments/fallback.webp",
			"https://cdn.discordapp.com/attachments/replied.jpg",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("garminAIUserMessage() = %#v, want %#v", got, want)
	}
	bot.garminContextCutoffs = map[string]string{"100000000000000005": "250"}
	if got := bot.garminAIUserMessage(message, "what is this?"); len(got.Images) != 2 {
		t.Fatalf("pre-cutoff reply images = %v, want only current-message images", got.Images)
	}
}

func TestGarminAIUserMessageDefaultsImageOnlyPrompt(t *testing.T) {
	message := &discord.Message{
		Attachments: []discord.Attachment{{
			Filename: "photo.jpg", ContentType: ptr("image/jpeg"), URL: "https://cdn.discordapp.com/photo.jpg",
		}},
	}
	got := garminAIUserMessage(message, "")
	if got.Content != "what is in this image?" || len(got.Images) != 1 {
		t.Fatalf("image-only message = %#v", got)
	}
}

func TestHandleGarminAIBuildsDiscordContextOnlyOnce(t *testing.T) {
	for _, count := range []int{1, garminAICompactionMessages + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			session := mustTestClient(t)
			cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("12")}, Roles: []discord.Role{{ID: snowflakeID("13"), Name: "member"}}}})
			session.Caches.AddChannel(testChannel(t, map[string]any{"id": "5", "guild_id": "12", "name": "bots"}))
			memberLookups, completions := 0, 0
			session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch {
				case strings.Contains(r.URL.Path, "/members/"):
					memberLookups++
					body = `{"user":{"id":"56","username":"speaker"},"roles":[]}`
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages"):
					body = `[]`
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
					body = `{"id":"150","channel_id":"5","content":"hello"}`
				case strings.HasSuffix(r.URL.Path, "/typing"):
				default:
					t.Errorf("unexpected REST request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{Logger: zap.NewNop(), garminAISlots: make(chan struct{}, 1), garminAILastUsed: make(map[string]time.Time)}
			b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				completions++
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "hello"}}, nil
			})
			message := &discord.Message{ID: snowflakeID("100"), GuildID: snowflakePtr("12"), ChannelID: snowflakeID("5"), Content: "garmin, hello", Author: discord.User{ID: snowflakeID("56"), Username: "speaker"}, CreatedAt: time.Now().UTC()}
			messages := make([]cmd.GarminAIMessage, count)
			for index := range messages {
				messages[index] = garminAIUserMessage(message, "hello")
			}
			b.handleGarminAI(session, message, messages)
			wantCompletions := 1
			if count > garminAICompactionMessages {
				wantCompletions++
			}
			if memberLookups != 1 || completions != wantCompletions {
				t.Fatalf("member lookups = %d, completions = %d; want 1 and %d", memberLookups, completions, wantCompletions)
			}
			stored, ok := b.garminAIHistory(message, "150", false)
			if !ok || stored[len(stored)-1].Timestamp.IsZero() {
				t.Fatal("reply did not retain its timestamp")
			}
		})
	}
}

func TestSendGarminReplyRetriesWithoutReference(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if requests == 1 {
			if _, ok := payload["message_reference"]; !ok {
				t.Error("first request omitted message reference")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Unknown message","code":10008}`))
			return
		}
		if _, ok := payload["message_reference"]; ok {
			t.Error("fallback request included message reference")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"100000000000000004","channel_id":"100000000000000005","content":"answer"}`))
	}))
	defer server.Close()

	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Rest = testREST(server.Client())
	session.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: session.Rest.HTTPClient().Transport, target: server.URL}
	bot := &Bot{Logger: zap.NewNop()}
	reply := bot.sendGarminReply(session, &discord.Message{
		ID: snowflakeID("100000000000000029"), ChannelID: snowflakeID("100000000000000005"),
	}, "answer")
	if reply == nil || reply.ID != snowflakeID("100000000000000004") || requests != 2 {
		t.Fatalf("sendGarminReply() = %#v after %d requests", reply, requests)
	}
}

type rewriteDiscordTransport struct {
	base   http.RoundTripper
	target string
}

func (r rewriteDiscordTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme = "http"
	request.URL.Host = strings.TrimPrefix(r.target, "http://")
	return r.base.RoundTrip(request)
}

type summaryGarminAI struct {
	summary string
	request cmd.GarminAIRequest
}

func (f *summaryGarminAI) Complete(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
	f.request = request
	return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: f.summary}}, nil
}

type fakeGarminAI struct{}

func (*fakeGarminAI) Complete(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
	return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "ok"}}, nil
}
