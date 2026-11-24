package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExampleConfigurationLoads(t *testing.T) {
	if _, err := Load("../config.example.json"); err != nil {
		t.Fatal(err)
	}
}

func TestNestedConnectionsAndFeatureSettings(t *testing.T) {
	cfg := validConfig()
	cfg.PermaAdminDiscordIDs, cfg.PermaAdminTelegramIDs = nil, nil
	cfg.DiscordToken, cfg.TelegramToken, cfg.DiscordGuildID, cfg.TelegramChatID = "", "", "", 0
	cfg.Discord = DiscordConfig{Token: "nested-discord", GuildID: "456", AI: DiscordAIConfig{SupportChannelID: "789"}}
	cfg.Telegram = TelegramConfig{Token: "nested-telegram", ChatID: -100}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DiscordToken != "nested-discord" || loaded.DiscordGuildID != "456" || loaded.TelegramChatID != -100 || loaded.Discord.AI.SupportChannelID != "789" {
		t.Fatal("nested connection and routing settings were not applied")
	}
}

func TestLegacyConnectionsLoadWithoutBotAdministrators(t *testing.T) {
	cfg := validConfig()
	cfg.PermaAdminDiscordIDs, cfg.PermaAdminTelegramIDs = nil, nil
	cfg.StarboardChannelID, cfg.StarboardThreshold = "987", 5
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Discord.Token != "discord" || cfg.Telegram.ChatID != 1 || cfg.Discord.Starboard.ChannelID != "987" || cfg.Discord.Starboard.Threshold != 5 {
		t.Fatal("legacy keys were not normalized")
	}
	if cfg.Discord.AI.SupportChannelID != "" || cfg.Discord.Logging.ServerChannelID != "" || cfg.Discord.Welcome.RoleID != "" || cfg.Discord.Moderation.BlockedGuildID != "" || len(cfg.Discord.Starboard.IgnoredCategoryIDs) != 0 {
		t.Fatal("deployment-specific IDs must not be injected as defaults")
	}
}

func TestNestedSettingsTakePrecedence(t *testing.T) {
	cfg := validConfig()
	cfg.Discord = DiscordConfig{Token: "override", GuildID: "456", Starboard: DiscordStarboardConfig{ChannelID: "789", Threshold: 7}}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.DiscordToken != "override" || cfg.DiscordGuildID != "456" || cfg.StarboardChannelID != "789" || cfg.StarboardThreshold != 7 {
		t.Fatal("legacy aliases overrode nested settings")
	}
}

func TestExplicitEmptyNestedChannelDisablesLegacyDestination(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"starboard_channel_id":"123","discord_log_channel":"456","discord":{"starboard":{"channel_id":""},"logging":{"case_channel_id":""}}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.ApplyDefaults()
	if cfg.Discord.Starboard.ChannelID != "" || cfg.StarboardChannelID != "" || cfg.Discord.Logging.CaseChannelID != "" || cfg.DiscordLogChannel != "" {
		t.Fatal("legacy destination overrode an explicitly disabled feature")
	}
}

func TestExplicitEmptyNestedConnectionDoesNotUseLegacyCredentials(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"discord_token":"legacy-discord","telegram_token":"telegram","github_token":"github","discord_guild_id":"123","telegram_chat_id":-100,"github_owner":"owner","github_repo":"repo","log_file":"bot.log","discord":{"token":""}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "discord.token") {
		t.Fatalf("empty nested connection used legacy credentials: %v", err)
	}
}

func TestInvalidFeatureConfigurationIsRejected(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Config)
		field  string
	}{
		{"bad ID", func(c *Config) { c.Discord.AI.SupportChannelID = "not-an-id" }, "support_channel_id"},
		{"partial welcome", func(c *Config) { c.Discord.Welcome.RoleID = "123" }, "discord.welcome"},
		{"empty readable ID", func(c *Config) { c.Discord.AI.ReadableChannels = map[string]string{"news": ""} }, "readable_channels"},
		{"negative threshold", func(c *Config) { c.Discord.Starboard.Threshold = -1 }, "threshold"},
		{"empty reaction reason", func(c *Config) { c.Discord.Moderation.ProhibitedReactions = map[string]string{"x": ""} }, "prohibited_reactions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.change(&cfg)
			if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}
