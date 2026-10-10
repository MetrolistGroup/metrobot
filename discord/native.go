package discord

import (
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
)

func ptr[T any](value T) *T { return &value }

func value[T any](pointer *T) (zero T) {
	if pointer != nil {
		return *pointer
	}
	return zero
}

// Persisted/config IDs remain decimal strings; an invalid ID cannot address a Discord entity.
func snowflakeID(id string) snowflake.ID {
	parsed, _ := snowflake.Parse(id)
	return parsed
}

func snowflakeString(id *snowflake.ID) string {
	if id == nil || *id == 0 {
		return ""
	}
	return id.String()
}

func snowflakePtr(id string) *snowflake.ID {
	if parsed := snowflakeID(id); parsed != 0 {
		return &parsed
	}
	return nil
}

func updateTimeout(client *bot.Client, guildID, userID snowflake.ID, until *time.Time) error {
	_, err := client.Rest.UpdateMember(guildID, userID, discord.MemberUpdate{
		CommunicationDisabledUntil: omit.New(until),
	})
	return err
}

func updateNickname(client *bot.Client, guildID, userID snowflake.ID, nickname string) error {
	_, err := client.Rest.UpdateMember(guildID, userID, discord.MemberUpdate{Nick: &nickname})
	return err
}

func isThread(channel discord.GuildChannel) bool {
	_, ok := channel.(discord.GuildThread)
	return ok
}

func interactionMember(i discord.Interaction) *discord.Member {
	if member := i.Member(); member != nil {
		return &member.Member
	}
	return nil
}
