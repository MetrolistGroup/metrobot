package discord

import "github.com/MetrolistGroup/metrobot/config"

// discordSettings returns the per-bot Discord deployment settings. A bot
// without config gets empty settings, which disable every ID-based feature.
func (b *Bot) discordSettings() config.DiscordConfig {
	if b == nil || b.Config == nil {
		return config.DiscordConfig{}
	}
	return b.Config.Discord
}

// configuredID matches only a non-empty configured ID.
func configuredID(configured, id string) bool {
	return configured != "" && id == configured
}

func (b *Bot) isSupportChannel(channelID string) bool {
	return configuredID(b.discordSettings().AI.SupportChannelID, channelID)
}

func (b *Bot) isGeneralChannel(channelID string) bool {
	return configuredID(b.discordSettings().AI.GeneralChannelID, channelID)
}

func (b *Bot) isChatThread(channelID string) bool {
	return configuredID(b.discordSettings().AI.ChatThreadID, channelID)
}
