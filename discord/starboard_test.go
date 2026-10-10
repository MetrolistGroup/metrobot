package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
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

func TestCleanerWebhookRepostsCanCreateAndUpdateBothBoards(t *testing.T) {
	for _, board := range []reactionBoard{{"starboard", defaultStarboardEmoji}, {"sobboard", sobboardEmoji}} {
		t.Run(board.name, func(t *testing.T) {
			database, err := db.Open(filepath.Join(t.TempDir(), "boards.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			client := mustTestClient(t)
			client.Caches.AddChannel(testChannel(t, map[string]any{"id": "200", "guild_id": "300", "type": discord.ChannelTypeGuildText}))
			source := discord.Message{
				ID: 100, ChannelID: 200, Author: discord.User{ID: 700, Username: "source author", Bot: true},
				WebhookID: ptr(snowflake.ID(700)), Content: "watch https://youtu.be/video",
				Reactions: []discord.MessageReaction{{Count: 3, Emoji: discord.Emoji{Name: board.emoji}}},
			}
			posts, updates := 0, 0
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := []byte(`{"id":"800","channel_id":"500"}`)
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v10/channels/200/messages/100":
					var err error
					body, err = json.Marshal(source)
					if err != nil {
						t.Error(err)
					}
				case "POST /api/v10/channels/500/messages":
					posts++
					var payload discord.MessageCreate
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if len(payload.Embeds) != 1 || payload.Embeds[0].Description != source.Content {
						t.Errorf("repost content missing from board: %#v", payload)
					}
				case "GET /api/v10/channels/500/messages/800":
				case "PATCH /api/v10/channels/500/messages/800":
					updates++
					var payload discord.MessageUpdate
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if !strings.HasPrefix(value(payload.Content), board.emoji+" 4 |") {
						t.Errorf("repost reaction count was not updated: %#v", payload)
					}
				default:
					t.Errorf("unexpected board request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
			})})
			b := &Bot{DB: database, Config: &config.Config{DiscordGuildID: "300", StarboardChannelID: "500"}, Logger: zap.NewNop()}
			reaction := &events.MessageReactionAdd{GenericReaction: &events.GenericReaction{
				MessageID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), UserID: 400,
				Emoji: discord.PartialEmoji{Name: ptr(board.emoji)},
			}}
			b.handleReactionAdd(client, reaction)
			entry, err := database.GetBoardEntry(board.name, "100")
			if err != nil || entry == nil || value(entry.StarboardMsgID) != "800" || entry.StarCount != 3 || posts != 1 {
				t.Fatalf("repost did not reach %s: entry=%#v, posts=%d, err=%v", board.name, entry, posts, err)
			}
			source.Reactions[0].Count = 4
			b.handleReactionAdd(client, reaction)
			entry, err = database.GetBoardEntry(board.name, "100")
			if err != nil || entry == nil || entry.StarCount != 4 || posts != 1 || updates != 1 {
				t.Fatalf("repost board entry was not updated: entry=%#v, posts=%d, updates=%d, err=%v", entry, posts, updates, err)
			}
		})
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
