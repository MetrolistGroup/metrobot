package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

type fixtureGateway struct{ gateway.Gateway }

func (fixtureGateway) ShardID() int { return 0 }

func nativeLogFixture(t *testing.T) (*Bot, chan discord.MessageCreate) {
	t.Helper()
	b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop(), ready: make(chan struct{})}
	client := mustTestClient(t, bot.WithEventManagerConfigOpts(bot.WithGatewayHandlers(b.gatewayHandlers())))
	b.Client = client
	logged := make(chan discord.MessageCreate, 16)
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/channels/"+messageLogChannelID+"/messages") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		var message discord.MessageCreate
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Error(err)
		}
		logged <- message
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"900"}`)), Request: r}, nil
	})})
	b.installListeners()
	return b, logged
}

func nextNativeLog(t *testing.T, logged <-chan discord.MessageCreate) discord.MessageCreate {
	t.Helper()
	select {
	case message := <-logged:
		if message.AllowedMentions == nil || len(message.AllowedMentions.Parse) != 0 || message.AllowedMentions.RepliedUser {
			t.Fatalf("audit log allowed mentions: %#v", message.AllowedMentions)
		}
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("audit log was not sent")
		return discord.MessageCreate{}
	}
}

func dispatchPartialMessage(b *Bot, data string) {
	b.Client.EventManager.HandleGatewayEvent(fixtureGateway{}, gateway.EventTypeRaw, 1, gateway.EventRaw{
		EventType: gateway.EventTypeMessageUpdate, Payload: strings.NewReader(data),
	})
}

func TestNativeListenersPreserveGatewayEventManagerAndDispatchReady(t *testing.T) {
	client := mustTestClient(t)
	b := &Bot{Client: client, Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop(), ready: make(chan struct{})}
	manager := client.EventManager
	// disgo binds this method when it constructs the gateway.
	dispatch := manager.HandleGatewayEvent
	b.installListeners()
	if client.EventManager != manager {
		t.Fatal("listener installation replaced the event manager bound to the gateway")
	}
	dispatch(fixtureGateway{}, gateway.EventTypeReady, 1, gateway.EventReady{
		User: discord.OAuth2User{User: discord.User{ID: 900, Username: "fixture bot"}},
	})
	select {
	case <-b.ready:
	default:
		t.Fatal("gateway READY did not reach the startup listener")
	}
	if client.ID() != 900 {
		t.Fatal("READY did not populate the self-user cache")
	}
}

func TestNativePartialUpdatesPreserveOmittedFieldsAndClearExplicitFields(t *testing.T) {
	b, logged := nativeLogFixture(t)
	original := &discord.Message{
		ID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Author: discord.User{ID: 400, Username: "author"},
		Content: "original text", CreatedAt: time.Now().UTC().Truncate(time.Second),
		Attachments: []discord.Attachment{{ID: 500, Filename: "evidence.txt", URL: "https://example.com/evidence.txt"}},
		Embeds:      []discord.Embed{{Title: "original embed"}},
		Components: []discord.LayoutComponent{discord.ActionRowComponent{Components: []discord.InteractiveComponent{
			discord.ButtonComponent{CustomID: "evidence", Label: "Evidence", Style: discord.ButtonStylePrimary},
		}}},
	}
	b.rememberMessageForLogs(b.Client, original)
	dispatchPartialMessage(b, `{"id":"100","channel_id":"200","content":"edited text"}`)
	message := nextNativeLog(t, logged)
	for _, expected := range []string{"original text", "edited text", "evidence.txt", "original embed", "Evidence", "<@400>"} {
		if !strings.Contains(message.Content, expected) {
			t.Errorf("edit omitted %q: %s", expected, message.Content)
		}
	}
	cached, ok := b.cachedMessage("200", "100", false)
	if !ok || cached.Message == nil || cached.Message.Author.ID != 400 || !cached.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("partial update lost identity/timestamp: %#v", cached)
	}

	dispatchPartialMessage(b, `{"id":"100","channel_id":"200","attachments":[],"embeds":[],"components":[]}`)
	_ = nextNativeLog(t, logged)
	cached, _ = b.cachedMessage("200", "100", false)
	if cached.Message.Content != "edited text" || len(cached.Message.Attachments) != 0 || len(cached.Message.Embeds) != 0 || len(cached.Message.Components) != 0 {
		t.Fatalf("explicit clearing or omitted content was lost: %#v", cached.Message)
	}

	dispatchPartialMessage(b, `{"id":"100","channel_id":"200","content":""}`)
	_ = nextNativeLog(t, logged)
	cached, _ = b.cachedMessage("200", "100", false)
	if cached.Message.Content != "" {
		t.Fatal("explicit empty content did not clear the cache")
	}
	dispatchPartialMessage(b, `{"id":"100","channel_id":"200","edited_timestamp":"2026-01-01T00:00:00Z"}`)
	if len(logged) != 0 {
		t.Fatal("metadata-only update emitted a duplicate edit log")
	}
	if err := b.onRawMessageUpdate(b.Client, &events.Raw{EventRaw: gateway.EventRaw{Payload: strings.NewReader(`{"content":"missing IDs"}`)}}); err == nil {
		t.Fatal("malformed partial update was accepted")
	}
}

func TestNativeDeleteAndBulkDeleteUseFilteredBoundedCache(t *testing.T) {
	b, logged := nativeLogFixture(t)
	for _, message := range []discord.Message{
		{ID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Author: discord.User{ID: 400}, Content: "first evidence"},
		{ID: 101, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Author: discord.User{ID: 400}, Content: "second evidence"},
		{ID: 102, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Author: discord.User{ID: 401, Bot: true}, Content: "private bot content"},
		{ID: 103, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Author: discord.User{ID: 402}, WebhookID: ptr(snowflake.ID(700)), Content: "private webhook content"},
	} {
		if message.Author.Bot || message.WebhookID != nil {
			b.Client.EventManager.HandleGatewayEvent(fixtureGateway{}, gateway.EventTypeMessageCreate, 1, gateway.EventMessageCreate{Message: message})
		} else {
			b.rememberMessageForLogs(b.Client, &message)
		}
	}
	for _, id := range []string{"102", "103"} {
		cached, _ := b.cachedMessage("200", id, false)
		if cached.Message != nil || cached.Payload != "" {
			t.Fatal("bot/webhook content was retained")
		}
		dispatchPartialMessage(b, `{"id":"`+id+`","channel_id":"200","content":"edited bot content"}`)
	}
	b.Client.EventManager.HandleGatewayEvent(fixtureGateway{}, gateway.EventTypeMessageDelete, 2, gateway.EventMessageDelete{
		ID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)),
	})
	if message := nextNativeLog(t, logged); !strings.Contains(message.Content, "first evidence") {
		t.Fatal("delete lost cached message content")
	}
	b.Client.Caches.AddChannel(testChannel(t, map[string]any{"id": "200", "guild_id": "300", "parent_id": "600", "type": discord.ChannelTypeGuildPublicThread, "message_count": 4}))
	b.Client.EventManager.HandleGatewayEvent(fixtureGateway{}, gateway.EventTypeMessageDeleteBulk, 3, gateway.EventMessageDeleteBulk{
		IDs: []snowflake.ID{101, 102, 103, 104}, ChannelID: 200, GuildID: ptr(snowflake.ID(300)),
	})
	message := nextNativeLog(t, logged)
	if !strings.Contains(message.Content, "second evidence") || !strings.Contains(message.Content, "**Messages:** 2") || strings.Contains(message.Content, "private") || strings.Contains(message.Content, "edited bot") {
		t.Fatalf("bulk delete filtering/content: %s", message.Content)
	}
	if len(b.messageLogCache["200"]) != 0 {
		t.Fatal("deleted messages remained cached")
	}
	if thread, ok := b.Client.Caches.GuildThread(200); !ok || thread.MessageCount != 0 {
		t.Fatal("bulk deletion left the native thread cache stale")
	}
	for i := 0; i <= messageLogCachePerChannel; i++ {
		b.rememberMessageForLogs(b.Client, &discord.Message{ID: snowflake.ID(i + 1), ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Author: discord.User{ID: 400}})
	}
	if len(b.messageLogCache["200"]) != messageLogCachePerChannel {
		t.Fatal("message cache is not bounded")
	}
	if _, found := b.cachedMessage("200", "1", false); found {
		t.Fatal("oldest message was not evicted")
	}
	b.Client.Caches.AddChannel(testChannel(t, map[string]any{"id": "600", "guild_id": "300", "parent_id": messageLogChannelID, "type": discord.ChannelTypeGuildPublicThread}))
	for _, message := range []discord.Message{
		{ID: 1, ChannelID: 600, GuildID: ptr(snowflake.ID(300)), Author: discord.User{ID: 400}},
		{ID: 1, ChannelID: 601, GuildID: ptr(snowflake.ID(301)), Author: discord.User{ID: 400}},
	} {
		b.rememberMessageForLogs(b.Client, &message)
		if _, found := b.cachedMessage(message.ChannelID.String(), "1", false); found {
			t.Fatal("excluded channel/guild entered the audit cache")
		}
	}
}

func TestNativePartialEditsKeepBotAndWebhookIdentity(t *testing.T) {
	for _, test := range []struct {
		name    string
		bot     bool
		webhook bool
	}{
		{"bot", true, false},
		{"webhook", false, true},
		{"bot webhook", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, logged := nativeLogFixture(t)
			original := &discord.Message{
				ID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)),
				Author: discord.User{ID: 400, Bot: test.bot}, Content: "private original",
			}
			if test.webhook {
				original.WebhookID = ptr(snowflake.ID(700))
			}
			b.rememberMessageForLogs(b.Client, original)
			for _, author := range []string{`{"id":"400"}`, `{"id":"400","bot":false}`} {
				dispatchPartialMessage(b, `{"id":"100","channel_id":"200","guild_id":"300","author":`+author+`,"content":"private edit"}`)
				cached, found := b.cachedMessage("200", "100", false)
				if !found || cached.Bot != test.bot || cached.Webhook != test.webhook || cached.Message != nil || cached.Payload != "" {
					t.Fatalf("partial edit lost filtered identity or retained private content: %#v", cached)
				}
			}
			b.onMessageDeleteLog(b.Client, &events.MessageDelete{GenericMessage: &events.GenericMessage{
				MessageID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)),
			}})
			if len(logged) != 0 {
				t.Fatal("bot/webhook edit or deletion was logged")
			}
		})
	}
}

func TestNativeMemberUpdatesExposeOldStateBeforeCacheReplacement(t *testing.T) {
	client := mustTestClient(t)
	client.Caches.AddMember(discord.Member{GuildID: 300, User: discord.User{ID: 400}, Nick: ptr("old name"), RoleIDs: []snowflake.ID{500}})
	var update *events.GuildMemberUpdate
	client.AddEventListeners(bot.NewListenerFunc(func(event *events.GuildMemberUpdate) { update = event }))
	client.EventManager.HandleGatewayEvent(fixtureGateway{}, gateway.EventTypeGuildMemberUpdate, 1, gateway.EventGuildMemberUpdate{
		Member: discord.Member{GuildID: 300, User: discord.User{ID: 400}, RoleIDs: []snowflake.ID{501}},
	})
	current, ok := client.Caches.Member(300, 400)
	if update == nil || value(update.OldMember.Nick) != "old name" || !slices.Equal(update.OldMember.RoleIDs, []snowflake.ID{500}) || update.Member.Nick != nil || !ok || !slices.Equal(current.RoleIDs, []snowflake.ID{501}) {
		t.Fatalf("old-state or nickname-removal semantics were lost: %#v", update)
	}
}

func TestNativeAuditPermissionChangesDecodeRawJSON(t *testing.T) {
	var entry discord.AuditLogEntry
	if err := json.Unmarshal([]byte(`{"changes":[{"key":"permissions","old_value":"1024","new_value":"3072"},{"key":"name","old_value":"before","new_value":"after"}]}`), &entry); err != nil {
		t.Fatal(err)
	}
	formatted := formatAuditChanges(entry.Changes)
	for _, expected := range []string{"Permissions added", "Send Messages", "before", "after"} {
		if !strings.Contains(formatted, expected) {
			t.Errorf("native audit changes omitted %q: %s", expected, formatted)
		}
	}
}
