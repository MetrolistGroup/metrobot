package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const DefaultOpenRouterModel = "z-ai/glm-5.3-flash:floor"

type Config struct {
	Discord  DiscordConfig  `json:"discord"`
	Telegram TelegramConfig `json:"telegram"`

	// Legacy connection keys remain accepted; nested settings take precedence.
	DiscordToken            string   `json:"discord_token"`
	TelegramToken           string   `json:"telegram_token"`
	GitHubToken             string   `json:"github_token"`
	OpenRouterAPIKeys       []string `json:"openrouter_api_keys"`
	OpenRouterModel         string   `json:"openrouter_model"`
	OpenRouterDecisionModel string   `json:"openrouter_decision_model"`
	FirecrawlAPIKeys        []string `json:"firecrawl_api_keys"`
	WolframAppIDs           []string `json:"wolfram_app_ids"`
	GarminMemoryFile        string   `json:"garmin_memory_file"`

	DiscordGuildID string `json:"discord_guild_id"`
	TelegramChatID int64  `json:"telegram_chat_id"`

	// Deprecated and ignored. Platform permissions determine access.
	PermaAdminDiscordIDs  []string `json:"permaadmin_discord_ids"`
	PermaAdminTelegramIDs []string `json:"permaadmin_telegram_ids"`

	GitHubOwner string `json:"github_owner"`
	GitHubRepo  string `json:"github_repo"`
	// Deprecated Actions keys remain accepted for existing installations.
	ActionsWorkflowFile  string `json:"actions_workflow_file"`
	ActionsArtifactNames struct {
		Foss string `json:"foss"`
		GMS  string `json:"gms"`
	} `json:"actions_artifact_names"`

	LogFile           string `json:"log_file"`
	DiscordLogChannel string `json:"discord_log_channel"`

	// Starboard configuration
	StarboardChannelID string `json:"starboard_channel_id"`
	StarboardThreshold int    `json:"starboard_threshold"`
	StarboardEmoji     string `json:"starboard_emoji"`

	defaultsApplied bool
}

// UnmarshalJSON also honors explicitly empty nested values over legacy keys.
func (c *Config) UnmarshalJSON(data []byte) error {
	type plain Config
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var supplied struct {
		Discord  map[string]json.RawMessage `json:"discord"`
		Telegram map[string]json.RawMessage `json:"telegram"`
	}
	if err := json.Unmarshal(data, &supplied); err != nil {
		return err
	}
	*c = Config(decoded)
	for _, field := range []struct {
		name   string
		legacy *string
		value  string
	}{
		{"token", &c.DiscordToken, c.Discord.Token},
		{"guild_id", &c.DiscordGuildID, c.Discord.GuildID},
	} {
		if _, present := supplied.Discord[field.name]; present {
			*field.legacy = field.value
		}
	}
	if _, present := supplied.Telegram["token"]; present {
		c.TelegramToken = c.Telegram.Token
	}
	if _, present := supplied.Telegram["chat_id"]; present {
		c.TelegramChatID = c.Telegram.ChatID
	}
	var starboard, logging map[string]json.RawMessage
	if raw, present := supplied.Discord["starboard"]; present {
		if err := json.Unmarshal(raw, &starboard); err != nil {
			return err
		}
	}
	if raw, present := supplied.Discord["logging"]; present {
		if err := json.Unmarshal(raw, &logging); err != nil {
			return err
		}
	}
	if _, present := starboard["channel_id"]; present {
		c.StarboardChannelID = c.Discord.Starboard.ChannelID
	}
	if _, present := starboard["threshold"]; present {
		c.StarboardThreshold = c.Discord.Starboard.Threshold
	}
	if _, present := starboard["emoji"]; present {
		c.StarboardEmoji = c.Discord.Starboard.Emoji
	}
	if _, present := logging["case_channel_id"]; present {
		c.DiscordLogChannel = c.Discord.Logging.CaseChannelID
	}
	return nil
}

// Load reads config.json from the given path and validates all required fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	c.ApplyDefaults()
	c.OpenRouterModel = strings.TrimSpace(c.OpenRouterModel)
	if c.OpenRouterModel == "" {
		c.OpenRouterModel = DefaultOpenRouterModel
	}
	c.OpenRouterDecisionModel = strings.TrimSpace(c.OpenRouterDecisionModel)
	checks := []struct {
		name  string
		value string
	}{
		{"discord.token", c.DiscordToken},
		{"telegram.token", c.TelegramToken},
		{"github_token", c.GitHubToken},
		{"discord.guild_id", c.DiscordGuildID},
		{"github_owner", c.GitHubOwner},
		{"github_repo", c.GitHubRepo},
		{"log_file", c.LogFile},
	}

	for _, ch := range checks {
		if ch.value == "" {
			return fmt.Errorf("required field %q is empty", ch.name)
		}
	}

	if c.TelegramChatID == 0 {
		return fmt.Errorf("required field %q is zero", "telegram.chat_id")
	}

	if err := c.Discord.validate(); err != nil {
		return err
	}
	if c.OpenRouterDecisionModel != "" && len(c.OpenRouterAPIKeys) == 0 {
		return fmt.Errorf("openrouter_decision_model requires openrouter_api_keys")
	}
	for i, key := range c.OpenRouterAPIKeys {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("openrouter_api_keys[%d] is empty", i)
		}
	}
	for i, key := range c.FirecrawlAPIKeys {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("firecrawl_api_keys[%d] is empty", i)
		}
	}
	for i, key := range c.WolframAppIDs {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("wolfram_app_ids[%d] is empty", i)
		}
	}

	return nil
}
