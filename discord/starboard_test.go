package discord

import (
	"github.com/disgoorg/disgo/discord"
	"strings"
	"testing"
)

func TestBoardDescriptionIncludesReplyContext(t *testing.T) {
	msg := &discord.Message{
		ChannelID: snowflakeID("100000000000000005"),
		Content:   "the reply", MessageReference: &discord.MessageReference{
			MessageID: snowflakePtr("100000000000000029"),
			ChannelID: snowflakePtr("100000000000000005"),
		},
		ReferencedMessage: &discord.Message{
			ID:        snowflakeID("100000000000000029"),
			ChannelID: snowflakeID("100000000000000005"),
			Content:   "first line\nsecond line",
			Author:    discord.User{ID: snowflakeID("100000000000000056"), Username: "buh"},
		},
	}

	got := boardDescription(msg, "100000000000000012")
	for _, want := range []string{
		"Replying to [@buh](https://discord.com/channels/100000000000000012/100000000000000005/100000000000000029)",
		"> first line\n> second line",
		"\n\nthe reply",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("boardDescription() = %q, want it to contain %q", got, want)
		}
	}
	msg.ReferencedMessage.ID, msg.ReferencedMessage.ChannelID = 0, 0
	if got := boardDescription(msg, "100000000000000012"); !strings.Contains(got, "/100000000000000005/100000000000000029)") {
		t.Fatalf("missing reply IDs replaced the message reference: %s", got)
	}
}

func TestBoardCategoryExclusionsIncludeThreads(t *testing.T) {
	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}}})

	ignoredCategory := "1441100425595195442"
	for _, channel := range []discord.GuildChannel{testChannel(t, map[string]any{"id": "100000000000000008", "guild_id": "100000000000000012", "parent_id": ignoredCategory, "type": discord.ChannelTypeGuildText}), testChannel(t, map[string]any{"id": "100000000000000034", "guild_id": "100000000000000012", "parent_id": ignoredCategory, "type": discord.ChannelTypeGuildText}), testChannel(t, map[string]any{"id": "100000000000000054", "guild_id": "100000000000000012", "parent_id": "100000000000000034", "type": discord.ChannelTypeGuildPublicThread})} {
		session.Caches.AddChannel(channel)

	}

	for _, channelID := range []string{"100000000000000008", "100000000000000054"} {
		ignored, err := boardChannelIgnored(session, channelID)
		if err != nil {
			t.Fatal(err)
		}
		if !ignored {
			t.Fatalf("boardChannelIgnored(%q) = false, want true", channelID)
		}
	}
}
