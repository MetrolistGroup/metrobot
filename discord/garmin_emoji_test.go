package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
)

func TestGarminEmojiVisionIncludesMessageReplyAndReactions(t *testing.T) {
	m := lifecycleMessage("102")
	m.Content = "what are <:wave:1> <a:dance:2> <:wave:1> 👨‍👩‍👧‍👦 🏳️‍🌈 👍🏽 🇵🇱?"
	m.Reactions = []discord.MessageReaction{
		{Count: 3, Emoji: discord.Emoji{ID: snowflakeID("3"), Name: "happy"}},
		{Count: 2, Emoji: discord.Emoji{Name: "❤️"}},
	}
	m.ReferencedMessage = &discord.Message{
		ID: snowflakeID("101"), ChannelID: m.ChannelID, GuildID: m.GuildID,
		Author: discord.User{ID: snowflakeID("42")}, Content: "<a:spin:4>",
		Reactions: []discord.MessageReaction{{Count: 1, Emoji: discord.Emoji{ID: snowflakeID("5"), Name: "reply_reaction", Animated: true}}},
	}
	b := lifecycleBot(nil)
	message := b.garminAIUserMessage(m, m.Content)
	if message.Content != m.Content || len(message.Images) != garminAIMaxEmojiImages {
		t.Fatalf("text changed or image budget violated: %#v", message)
	}
	for i, imageURL := range message.Images {
		want := fmt.Sprintf("https://cdn.discordapp.com/emojis/%d.png?size=128&quality=lossless", i+1)
		if imageURL != want {
			t.Errorf("image %d = %q, want %q", i, imageURL, want)
		}
	}
	metadata := b.garminEmojiContext(m)
	if len(metadata["current_message"]) != 4 || len(metadata["replied_to_message"]) != 2 || !metadata["current_message"][1].Animated || metadata["current_message"][2].Count != 3 || metadata["current_message"][3].Name != "❤️" {
		t.Fatalf("emoji metadata lost: %#v", metadata)
	}
	b.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if !reflect.DeepEqual(request.Messages[0].Images, message.Images) {
			t.Errorf("model did not receive emoji images: %#v", request.Messages)
		}
		for _, name := range []string{"happy", "reply_reaction", "dance", "❤️"} {
			if !strings.Contains(request.Context, name) {
				t.Errorf("model context missing %q", name)
			}
		}
		encoded, err := json.Marshal(request.Messages[0])
		if err != nil || !strings.Contains(string(encoded), `"type":"image_url"`) || !strings.Contains(string(encoded), "👨‍👩‍👧‍👦 🏳️‍🌈 👍🏽 🇵🇱") {
			t.Errorf("wire input lost vision or Unicode: %s, %v", encoded, err)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "those are custom emojis."}}, nil
	})
	if _, err := b.runGarminAI(context.Background(), nil, m, []cmd.GarminAIMessage{message}); err != nil {
		t.Fatal(err)
	}
}

func TestGarminEmojiInputIsBoundedAndUsesValidatedIDs(t *testing.T) {
	if got := uniqueGarminAIImageURLs([]string{"https://example.com/image.png"}, 0); len(got) != 0 {
		t.Fatalf("zero image budget returned images: %v", got)
	}
	m := lifecycleMessage("102")
	m.Content = "<:invalid:0> <:invalid:18446744073709551616> <:" + strings.Repeat("x", 65) + ":1> "
	if got := garminMessageEmojis(m); len(got) != 0 {
		t.Fatalf("invalid emojis accepted: %#v", got)
	}
	for i := 1; i <= 50; i++ {
		m.Content += fmt.Sprintf(" <:emoji_%d:%d>", i, i)
	}
	if got := garminMessageEmojis(m); len(got) > garminAIMaxEmojiInputs {
		t.Fatalf("emoji metadata exceeded budget: %d", len(got))
	}
	if got := garminAIUserMessage(m, m.Content); len(got.Images) != garminAIMaxEmojiImages {
		t.Fatalf("emoji image budget = %d", len(got.Images))
	}
	latest := garminAIUserMessage(&discord.Message{Content: "<:latest:99>"}, "latest")
	messages := []cmd.GarminAIMessage{garminAIUserMessage(m, m.Content), latest}
	boundGarminEmojiImages(messages)
	if len(messages[0].Images)+len(messages[1].Images) != garminAIMaxEmojiImages || !strings.Contains(messages[1].Images[0], "/99.png") {
		t.Fatalf("request budget lost latest emoji or exceeded limit: %#v", messages)
	}
}

func TestGarminEmojiReplyVisibilityMatchesAttachmentVisibility(t *testing.T) {
	for _, mode := range []string{"reset", "deleted", "other channel", "other guild"} {
		t.Run(mode, func(t *testing.T) {
			b := lifecycleBot(&fakeGarminAI{})
			m := lifecycleMessage("102")
			m.Content = "what is this?"
			m.ReferencedMessage = &discord.Message{ID: snowflakeID("90"), GuildID: m.GuildID, ChannelID: m.ChannelID, Author: discord.User{ID: snowflakeID("42")}, Content: "<:private_emoji:2>"}
			switch mode {
			case "reset":
				b.garminContextCutoffs = map[string]string{m.ChannelID.String(): "100"}
			case "deleted":
				b.cancelDeletedGarminMessage(m.ChannelID.String(), "90")
			case "other channel":
				m.ReferencedMessage.ChannelID = snowflakeID("6")
			case "other guild":
				m.ReferencedMessage.GuildID = snowflakePtr("13")
			}
			if got := b.garminAIUserMessage(m, m.Content); len(got.Images) != 0 {
				t.Fatalf("invisible emoji image leaked: %#v", got)
			}
			if got := b.garminDiscordContextForMessage(nil, m); strings.Contains(got, "private_emoji") {
				t.Fatalf("invisible reply content leaked: %s", got)
			}
		})
	}
}
