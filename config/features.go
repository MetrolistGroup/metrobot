package config

import (
	"fmt"
	"strconv"
	"strings"
)

type DiscordConfig struct {
	Token      string                  `json:"token"`
	GuildID    string                  `json:"guild_id"`
	Logging    DiscordLoggingConfig    `json:"logging"`
	Moderation DiscordModerationConfig `json:"moderation"`
	Welcome    DiscordWelcomeConfig    `json:"welcome"`
	AI         DiscordAIConfig         `json:"ai"`
	Starboard  DiscordStarboardConfig  `json:"starboard"`
}

type TelegramConfig struct {
	Token  string `json:"token"`
	ChatID int64  `json:"chat_id"`
}

type DiscordLoggingConfig struct {
	CaseChannelID     string `json:"case_channel_id"`
	ServerChannelID   string `json:"server_channel_id"`
	MessageChannelID  string `json:"message_channel_id"`
	ReactionChannelID string `json:"reaction_channel_id"`
}

type DiscordModerationConfig struct {
	BlockedGuildID      string            `json:"blocked_guild_id"`
	ProhibitedReactions map[string]string `json:"prohibited_reactions"`
}

type DiscordWelcomeConfig struct {
	RoleID    string `json:"role_id"`
	ChannelID string `json:"channel_id"`
	Note      string `json:"note"`
}

type DiscordAIConfig struct {
	GeneralChannelID    string            `json:"general_channel_id"`
	BotsChannelID       string            `json:"bots_channel_id"`
	SupportChannelID    string            `json:"support_channel_id"`
	ChatThreadID        string            `json:"chat_thread_id"`
	ReadableChannels    map[string]string `json:"readable_channels"`
	ChannelDescriptions map[string]string `json:"channel_descriptions"`
}

type DiscordStarboardConfig struct {
	ChannelID          string   `json:"channel_id"`
	Threshold          int      `json:"threshold"`
	Emoji              string   `json:"emoji"`
	SobEmoji           string   `json:"sob_emoji"`
	IgnoredCategoryIDs []string `json:"ignored_category_ids"`
}

// ApplyDefaults resolves legacy aliases once before either platform starts.
func (c *Config) ApplyDefaults() {
	if c == nil || c.defaultsApplied {
		return
	}
	if c.Discord.Token == "" {
		c.Discord.Token = c.DiscordToken
	}
	if c.Discord.GuildID == "" {
		c.Discord.GuildID = c.DiscordGuildID
	}
	if c.Telegram.Token == "" {
		c.Telegram.Token = c.TelegramToken
	}
	if c.Telegram.ChatID == 0 {
		c.Telegram.ChatID = c.TelegramChatID
	}
	if c.Discord.Starboard.ChannelID == "" {
		c.Discord.Starboard.ChannelID = c.StarboardChannelID
	}
	if c.Discord.Starboard.Threshold == 0 {
		c.Discord.Starboard.Threshold = c.StarboardThreshold
	}
	if c.Discord.Starboard.Emoji == "" {
		c.Discord.Starboard.Emoji = c.StarboardEmoji
	}
	if c.Discord.Starboard.Threshold == 0 {
		c.Discord.Starboard.Threshold = 3
	}
	if c.Discord.Starboard.Emoji == "" {
		c.Discord.Starboard.Emoji = "⭐"
	}
	if c.Discord.Starboard.SobEmoji == "" {
		c.Discord.Starboard.SobEmoji = "😭"
	}
	if c.Discord.Logging.CaseChannelID == "" {
		c.Discord.Logging.CaseChannelID = c.DiscordLogChannel
	}
	c.DiscordToken, c.DiscordGuildID = c.Discord.Token, c.Discord.GuildID
	c.TelegramToken, c.TelegramChatID = c.Telegram.Token, c.Telegram.ChatID
	c.StarboardChannelID, c.StarboardThreshold, c.StarboardEmoji = c.Discord.Starboard.ChannelID, c.Discord.Starboard.Threshold, c.Discord.Starboard.Emoji
	c.DiscordLogChannel = c.Discord.Logging.CaseChannelID
	c.defaultsApplied = true
}

func (c DiscordConfig) validate() error {
	ids := map[string]string{
		"discord.guild_id":                    c.GuildID,
		"discord.logging.case_channel_id":     c.Logging.CaseChannelID,
		"discord.logging.server_channel_id":   c.Logging.ServerChannelID,
		"discord.logging.message_channel_id":  c.Logging.MessageChannelID,
		"discord.logging.reaction_channel_id": c.Logging.ReactionChannelID,
		"discord.moderation.blocked_guild_id": c.Moderation.BlockedGuildID,
		"discord.welcome.role_id":             c.Welcome.RoleID,
		"discord.welcome.channel_id":          c.Welcome.ChannelID,
		"discord.ai.general_channel_id":       c.AI.GeneralChannelID,
		"discord.ai.bots_channel_id":          c.AI.BotsChannelID,
		"discord.ai.support_channel_id":       c.AI.SupportChannelID,
		"discord.ai.chat_thread_id":           c.AI.ChatThreadID,
		"discord.starboard.channel_id":        c.Starboard.ChannelID,
	}
	for alias, id := range c.AI.ReadableChannels {
		if alias == "" || strings.TrimSpace(alias) != alias || strings.ContainsAny(alias, " \t\r\n") || id == "" {
			return fmt.Errorf("discord.ai.readable_channels requires nonempty aliases without whitespace and channel IDs")
		}
		ids["discord.ai.readable_channels."+alias] = id
	}
	for id := range c.AI.ChannelDescriptions {
		if id == "" {
			return fmt.Errorf("discord.ai.channel_descriptions requires channel IDs")
		}
		ids["discord.ai.channel_descriptions."+id] = id
	}
	for index, id := range c.Starboard.IgnoredCategoryIDs {
		if id == "" {
			return fmt.Errorf("discord.starboard.ignored_category_ids[%d] is empty", index)
		}
		ids[fmt.Sprintf("discord.starboard.ignored_category_ids[%d]", index)] = id
	}
	for field, id := range ids {
		if id == "" {
			continue
		}
		value, err := strconv.ParseUint(id, 10, 64)
		if err != nil || value == 0 {
			return fmt.Errorf("%s must be a positive Discord ID", field)
		}
	}
	if c.Starboard.Threshold < 1 {
		return fmt.Errorf("discord.starboard.threshold must be positive")
	}
	if c.Welcome.RoleID != "" || c.Welcome.ChannelID != "" || c.Welcome.Note != "" {
		if c.Welcome.RoleID == "" || c.Welcome.ChannelID == "" || strings.TrimSpace(c.Welcome.Note) == "" {
			return fmt.Errorf("discord.welcome requires role_id, channel_id and note together")
		}
	}
	for emoji, reason := range c.Moderation.ProhibitedReactions {
		if strings.TrimSpace(emoji) == "" || strings.TrimSpace(reason) == "" {
			return fmt.Errorf("discord.moderation.prohibited_reactions requires nonempty emoji and reason pairs")
		}
	}
	return nil
}
