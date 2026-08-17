package discord

import (
	"context"
	"encoding/json"
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
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

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
	history := []cmd.GarminAIMessage{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "one answer"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "two answer"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "three answer"},
	}
	original := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "user"},
	}}
	bot.rememberGarminAIContext("bot-message", original, history)

	messages, ok := bot.garminAIContinuation(&discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID:          "guild",
		ChannelID:        "channel",
		MessageReference: &discordgo.MessageReference{MessageID: "bot-message"},
		Author:           &discordgo.User{ID: "user"},
	}}, "  follow up  ")
	if !ok {
		t.Fatal("garminAIContinuation() did not recognize tracked reply")
	}
	want := []cmd.GarminAIMessage{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "one answer"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "two answer"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "three answer"},
		{Role: "user", Content: "follow up"},
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("messages = %#v, want %#v", messages, want)
	}
}

func TestGarminAIContinuationRejectsUntrackedAndExpiredReplies(t *testing.T) {
	bot := &Bot{
		garminAI: &fakeGarminAI{},
		garminAIContexts: map[string]garminAIContext{
			"expired": {userID: "user", channelID: "channel", expiresAt: time.Now().Add(-time.Minute)},
		},
	}
	for _, messageID := range []string{"other-bot-message", "expired"} {
		messages, ok := bot.garminAIContinuation(&discordgo.MessageCreate{Message: &discordgo.Message{
			GuildID:          "guild",
			ChannelID:        "channel",
			MessageReference: &discordgo.MessageReference{MessageID: messageID},
			Author:           &discordgo.User{ID: "user"},
		}}, "follow up")
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
	original := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "nyx"},
	}}
	bot.rememberGarminAIContext("tracked-bot-reply", original, []cmd.GarminAIMessage{
		{Role: "user", Content: "garmin, hello"},
		{Role: "assistant", Content: "hey nyx"},
	})
	messages, ok := bot.garminAIContinuation(&discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID:           "guild",
		ChannelID:         "channel",
		MessageReference:  &discordgo.MessageReference{MessageID: "human-message"},
		ReferencedMessage: &discordgo.Message{ID: "human-message", Author: &discordgo.User{ID: "human"}},
		Author:            &discordgo.User{ID: "nyx"},
	}}, "me and lamp")
	if ok || messages != nil {
		t.Fatalf("human reply woke Garmin with messages %#v", messages)
	}
}

func TestGarminAITriggeredConversationUsesActiveHistory(t *testing.T) {
	bot := &Bot{
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	history := []cmd.GarminAIMessage{
		{Role: "user", Content: "always add a heart"},
		{Role: "assistant", Content: "got it ❤️"},
	}
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "john"},
	}}
	bot.rememberGarminAIContext("old-reply", message, history)
	got := bot.garminAITriggeredConversation(message, "you forgot")
	want := append(history, cmd.GarminAIMessage{Role: "user", Content: "you forgot"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("triggered conversation = %#v, want %#v", got, want)
	}
}

func TestGarminAIContextAllowsReplyParticipantsButScopesAmbientToUser(t *testing.T) {
	bot := &Bot{
		garminAI:             &fakeGarminAI{},
		garminAIContexts:     make(map[string]garminAIContext),
		garminAIUserContexts: make(map[string]garminAIContext),
	}
	original := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "owner"},
	}}
	history := []cmd.GarminAIMessage{{Role: "user", Content: "public thread"}, {Role: "assistant", Content: "reply"}}
	bot.rememberGarminAIContext("bot-message", original, history)

	otherUser := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "other"},
		MessageReference: &discordgo.MessageReference{MessageID: "bot-message"},
	}}
	if messages, ok := bot.garminAIContinuation(otherUser, "show me"); !ok || len(messages) != 3 || messages[2].Content != "show me" {
		t.Fatalf("same-channel participant continuation = (%#v, %v)", messages, ok)
	}

	for _, attempt := range []*discordgo.MessageCreate{
		{Message: &discordgo.Message{GuildID: "guild", ChannelID: "other-channel", Author: &discordgo.User{ID: "owner"}, MessageReference: &discordgo.MessageReference{MessageID: "bot-message"}}},
		{Message: &discordgo.Message{GuildID: "other-guild", ChannelID: "channel", Author: &discordgo.User{ID: "owner"}, MessageReference: &discordgo.MessageReference{MessageID: "bot-message"}}},
	} {
		if messages, ok := bot.garminAIContinuation(attempt, "show me"); ok || messages != nil {
			t.Fatalf("cross-channel/guild reply exposed context: %#v", messages)
		}
	}

	sameUser := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "owner"},
		MessageReference: &discordgo.MessageReference{MessageID: "bot-message"},
	}}
	if _, ok := bot.garminAIContinuation(sameUser, "continue"); !ok {
		t.Fatal("authorized reply context was lost after rejected attempts")
	}

	ambient := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "owner"},
	}}
	messages, ok := bot.garminAIAmbientContinuation(ambient, "one more thing")
	if !ok || len(messages) != 3 || messages[2].Content != "one more thing" {
		t.Fatalf("ambient continuation = (%#v, %v)", messages, ok)
	}
	for _, attempt := range []*discordgo.MessageCreate{
		{Message: &discordgo.Message{GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "other"}}},
		{Message: &discordgo.Message{GuildID: "guild", ChannelID: "other-channel", Author: &discordgo.User{ID: "owner"}}},
		{Message: &discordgo.Message{GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "owner"}, MessageReference: &discordgo.MessageReference{MessageID: "human"}}},
	} {
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
	original := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "100", GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "user"}}}
	bot.rememberGarminAIContext("150", original, []cmd.GarminAIMessage{{Role: "user", Content: "old context"}})
	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	if !bot.registerGarminAIRequest(original, cancelRequest) {
		t.Fatal("failed to register pre-reset request")
	}
	if err := bot.resetGarminContext("channel", "200"); err != nil {
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
	if bot.garminMessageVisible("channel", "199") || !bot.garminMessageVisible("channel", "201") {
		t.Fatal("context cutoff did not hide only prior messages")
	}
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "201", GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "user", Username: "user"},
		ReferencedMessage: &discordgo.Message{ID: "199", Content: "old secret", Author: &discordgo.User{ID: "other", Username: "other"}},
	}}
	if context := bot.garminDiscordContextForMessage(nil, message); strings.Contains(context, "old secret") {
		t.Fatalf("old replied message survived cutoff: %s", context)
	}
}

func TestGarminAIAmbientReplyPolicy(t *testing.T) {
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	message := &discordgo.MessageCreate{Message: &discordgo.Message{Author: &discordgo.User{ID: "user"}}}
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
	message.Mentions = []*discordgo.User{{ID: "other"}}
	if !garminAIAmbientTargetsOtherUser(session, message) {
		t.Fatal("ambient message addressing another user was accepted")
	}
}

func TestGarminAIEmojiCatalogContainsAllCurrentGuildEmojis(t *testing.T) {
	if len(garminAIEmojis) != 46 {
		t.Fatalf("emoji catalog has %d entries, want 46", len(garminAIEmojis))
	}
	for name, emoji := range garminAIEmojis {
		if emoji.Name != name || emoji.ID == "" || !emoji.Available {
			t.Errorf("invalid emoji %q: %#v", name, emoji)
		}
	}
	for _, name := range []string{"soggy", "thumb", "painfade", "cozystars"} {
		if _, ok := garminAIEmojis[name]; !ok {
			t.Errorf("emoji catalog missing %q", name)
		}
	}
}

func TestEnforceGarminGeneralReplyIsShortAndRedirectsToBots(t *testing.T) {
	got := enforceGarminChannelReply(garminGeneralID, "yeah, sure. what's on your mind?")
	want := "yeah, sure. continue in <#" + garminBotsID + "> if you wanna chat more."
	if got != want {
		t.Fatalf("enforceGarminChannelReply() = %q, want %q", got, want)
	}
	if got := enforceGarminChannelReply("another-channel", "yeah, sure. what's on your mind?"); got != "yeah, sure. what's on your mind?" {
		t.Fatalf("non-general reply changed to %q", got)
	}
}

func TestEnforceGarminThreadUnderGeneralRedirectsToBots(t *testing.T) {
	state := discordgo.NewState()
	if err := state.GuildAdd(&discordgo.Guild{ID: "guild"}); err != nil {
		t.Fatal(err)
	}
	if err := state.ChannelAdd(&discordgo.Channel{ID: garminGeneralID, GuildID: "guild", Name: "general"}); err != nil {
		t.Fatal(err)
	}
	if err := state.ChannelAdd(&discordgo.Channel{ID: "general-thread", GuildID: "guild", ParentID: garminGeneralID, Name: "thread", Type: discordgo.ChannelTypeGuildPublicThread}); err != nil {
		t.Fatal(err)
	}
	session := &discordgo.Session{State: state}
	got := enforceGarminChannelReply(garminRedirectChannelID(session, "general-thread"), "yeah, sure. what's on your mind?")
	want := "yeah, sure. continue in <#" + garminBotsID + "> if you wanna chat more."
	if got != want {
		t.Fatalf("thread reply = %q, want %q", got, want)
	}
}

func TestEnforceGarminGeneralReplyDoesNotRedirectRefusalsOrDuplicateBots(t *testing.T) {
	for _, answer := range []string{
		"i can't do that.",
		"take it to <#" + garminBotsID + ">.",
		"take it to #bots.",
	} {
		if got := enforceGarminChannelReply(garminGeneralID, answer); got != answer {
			t.Errorf("enforceGarminChannelReply(%q) = %q", answer, got)
		}
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
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{
			{Filename: "photo.png", ContentType: "image/png", URL: "https://cdn.discordapp.com/attachments/photo.png"},
			{Filename: "notes.txt", ContentType: "text/plain", URL: "https://cdn.discordapp.com/attachments/notes.txt"},
			{Filename: "fallback.webp", URL: "https://media.discordapp.net/attachments/fallback.webp"},
		},
	}}
	got := garminAIUserMessage(message, "  what is this?  ")
	want := cmd.GarminAIMessage{
		Role:    "user",
		Content: "what is this?",
		Images: []string{
			"https://cdn.discordapp.com/attachments/photo.png",
			"https://media.discordapp.net/attachments/fallback.webp",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("garminAIUserMessage() = %#v, want %#v", got, want)
	}
}

func TestGarminAIUserMessageDefaultsImageOnlyPrompt(t *testing.T) {
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{{
			Filename: "photo.jpg", ContentType: "image/jpeg", URL: "https://cdn.discordapp.com/photo.jpg",
		}},
	}}
	got := garminAIUserMessage(message, "")
	if got.Content != "what is in this image?" || len(got.Images) != 1 {
		t.Fatalf("image-only message = %#v", got)
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
		_, _ = w.Write([]byte(`{"id":"bot-reply","channel_id":"channel","content":"answer"}`))
	}))
	defer server.Close()

	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{Logger: zap.NewNop()}
	reply := bot.sendGarminReply(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "original", ChannelID: "channel",
	}}, "answer")
	if reply == nil || reply.ID != "bot-reply" || requests != 2 {
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

type fakeGarminAI struct{}

func (*fakeGarminAI) Complete(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
	return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: "ok"}}, nil
}
