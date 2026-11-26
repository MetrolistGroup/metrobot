package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestGarminRecentParticipantAliasesRespectVisibility(t *testing.T) {
	b := &Bot{}
	message := &discord.Message{ID: 200, ChannelID: 5, GuildID: ptr(snowflake.ID(12)), CreatedAt: time.Now(), Author: discord.User{ID: 56, Username: "speaker"}, Content: "Garmin hug sel"}
	add := func(id, user snowflake.ID, nickname string) {
		b.storeCachedMessage(cacheLogMessage(&discord.Message{ID: id, ChannelID: 5, GuildID: message.GuildID, CreatedAt: time.Now(), Author: discord.User{ID: user, Username: "user" + user.String()}, Member: &discord.Member{Nick: &nickname}}))
	}
	add(150, 57, "Sel Altman")
	participants, aliases := b.garminRecentParticipants(message)
	if aliases["sel"] != "57" || participants["57"]["display_name"] != "Sel Altman" {
		t.Fatalf("recent nickname not recognized: %+v %+v", participants, aliases)
	}
	add(160, 58, "Selena")
	_, aliases = b.garminRecentParticipants(message)
	if aliases["sel"] != "" {
		t.Fatal("ambiguous nickname resolved")
	}
	b.garminDeletedMessages = map[string]time.Time{"5\x00160": time.Now().Add(time.Hour)}
	_, aliases = b.garminRecentParticipants(message)
	if aliases["sel"] != "57" {
		t.Fatal("deleted message still affected aliases")
	}
	b.garminContextCutoffs = map[string]string{"5": "150"}
	participants, aliases = b.garminRecentParticipants(message)
	if len(participants) != 0 || len(aliases) != 0 {
		t.Fatal("context reset leaked participants")
	}
	b.garminContextCutoffs["5"] = ""
	add(201, 59, "Selfuture")
	_, aliases = b.garminRecentParticipants(message)
	if aliases["sel"] != "57" {
		t.Fatal("future message affected identity")
	}
	b.Config = testConfig(config.Config{})
	b.Config.Discord.AI.SupportChannelID = "5"
	if context := garminDiscordContext(b, nil, message, nil); strings.Contains(context, "Sel Altman") || strings.Contains(context, "recent_channel_participants") {
		t.Fatal("support received participant context")
	}
}
