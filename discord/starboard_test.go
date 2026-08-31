package discord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestBoardDescriptionIncludesReplyContext(t *testing.T) {
	msg := &discordgo.Message{
		ChannelID: "channel",
		Content:   "the reply",
		MessageReference: &discordgo.MessageReference{
			MessageID: "original",
			ChannelID: "channel",
		},
		ReferencedMessage: &discordgo.Message{
			ID:        "original",
			ChannelID: "channel",
			Content:   "first line\nsecond line",
			Author:    &discordgo.User{Username: "buh"},
		},
	}

	got := boardDescription(msg, "guild")
	for _, want := range []string{
		"Replying to [@buh](https://discord.com/channels/guild/channel/original)",
		"> first line\n> second line",
		"\n\nthe reply",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("boardDescription() = %q, want it to contain %q", got, want)
		}
	}
}

func TestBoardCategoryExclusionsIncludeThreads(t *testing.T) {
	session, err := discordgo.New("")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild"}); err != nil {
		t.Fatal(err)
	}
	ignoredCategory := "1441100425595195442"
	for _, channel := range []*discordgo.Channel{
		{ID: "direct", GuildID: "guild", ParentID: ignoredCategory, Type: discordgo.ChannelTypeGuildText},
		{ID: "parent", GuildID: "guild", ParentID: ignoredCategory, Type: discordgo.ChannelTypeGuildText},
		{ID: "thread", GuildID: "guild", ParentID: "parent", Type: discordgo.ChannelTypeGuildPublicThread},
	} {
		if err := session.State.ChannelAdd(channel); err != nil {
			t.Fatal(err)
		}
	}

	for _, channelID := range []string{"direct", "thread"} {
		ignored, err := boardChannelIgnored(session, channelID)
		if err != nil {
			t.Fatal(err)
		}
		if !ignored {
			t.Fatalf("boardChannelIgnored(%q) = false, want true", channelID)
		}
	}
}
