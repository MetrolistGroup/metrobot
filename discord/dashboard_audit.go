package discord

import (
	"context"

	"github.com/MetrolistGroup/metrobot/auditsearch"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

type auditREST interface {
	GetChannel(snowflake.ID, ...rest.RequestOpt) (discord.Channel, error)
	GetMessages(snowflake.ID, snowflake.ID, snowflake.ID, snowflake.ID, int, ...rest.RequestOpt) ([]discord.Message, error)
}

type dashboardAuditHistory struct {
	cfg  *config.Config
	rest auditREST
}

// AuditHistory reuses the bot's authenticated, rate-limited REST client, never its token.
func (b *Bot) AuditHistory() auditsearch.History {
	h := &dashboardAuditHistory{cfg: b.Config}
	if b.Client != nil {
		h.rest = b.Client.Rest
	}
	return h
}

func (h *dashboardAuditHistory) Channel(ctx context.Context, source string) (auditsearch.Channel, error) {
	if h.cfg == nil || h.rest == nil {
		return auditsearch.Channel{}, auditsearch.ErrUnavailable
	}
	var id string
	switch source {
	case "message":
		id = messageLogChannelID
	case "reaction":
		id = reactionLogChannelID
	case "server":
		id = serverLogChannelID
	case "moderation":
		id = h.cfg.DiscordLogChannel
	default:
		return auditsearch.Channel{}, auditsearch.ErrUnavailable
	}
	channelID, err := snowflake.Parse(id)
	if err != nil || channelID == 0 {
		return auditsearch.Channel{}, auditsearch.ErrUnavailable
	}
	guildID, err := snowflake.Parse(h.cfg.DiscordGuildID)
	if err != nil || guildID == 0 {
		return auditsearch.Channel{}, auditsearch.ErrUnavailable
	}
	channel, err := h.rest.GetChannel(channelID, rest.WithCtx(ctx))
	if err != nil {
		return auditsearch.Channel{}, auditsearch.ErrFetch
	}
	guildChannel, ok := channel.(discord.GuildChannel)
	if !ok || guildChannel.GuildID() != guildID || guildChannel.ID() != channelID {
		return auditsearch.Channel{}, auditsearch.ErrUnavailable
	}
	return auditsearch.Channel{GuildID: guildID, ID: channelID}, nil
}

func (h *dashboardAuditHistory) Messages(ctx context.Context, channelID, before snowflake.ID, limit int) ([]discord.Message, error) {
	if h.rest == nil {
		return nil, auditsearch.ErrUnavailable
	}
	messages, err := h.rest.GetMessages(channelID, 0, before, 0, limit, rest.WithCtx(ctx))
	if err != nil {
		return nil, auditsearch.ErrFetch
	}
	return messages, nil
}
