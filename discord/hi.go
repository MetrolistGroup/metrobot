package discord

import (
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
)

const (
	hiWebhookName   = "the Hi"
	hiWebhookAvatar = "https://cdn.discordapp.com/emojis/1562440495542837268.webp?size=128"
)

func isHiMessage(m *discord.Message) bool {
	return (m.Content == "Hi" || m.Content == "hi") && len(m.Attachments) == 0 && len(m.StickerItems) == 0
}

func (b *Bot) sendHiWebhook(s *bot.Client, m *discord.Message) {
	webhook, threadID, err := b.messageWebhook(s, m.ChannelID.String(), hiWebhookName)
	if err != nil {
		b.Logger.Warn("failed to get Hi webhook")
		return
	}
	_, err = s.Rest.CreateWebhookMessage(webhook.ID(), webhook.Token, discord.WebhookMessageCreate{
		Content:         "Hi",
		Username:        hiWebhookName,
		AvatarURL:       hiWebhookAvatar,
		AllowedMentions: &discord.AllowedMentions{},
	}, rest.CreateWebhookMessageParams{Wait: true, ThreadID: snowflakeID(threadID)})
	if err != nil {
		b.Logger.Warn("failed to send Hi webhook reply")
	}
}
