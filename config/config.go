package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	DiscordToken  string `json:"discord_token"`
	TelegramToken string `json:"telegram_token"`
	GitHubToken   string `json:"github_token"`

	DiscordGuildID string `json:"discord_guild_id"`
	TelegramChatID int64  `json:"telegram_chat_id"`

	PermaAdminDiscordIDs  []string `json:"permaadmin_discord_ids"`
	PermaAdminTelegramIDs []string `json:"permaadmin_telegram_ids"`

	GitHubOwner          string `json:"github_owner"`
	GitHubRepo           string `json:"github_repo"`
	ActionsWorkflowFile  string `json:"actions_workflow_file"`
	ActionsArtifactNames struct {
		Foss string `json:"foss"`
		GMS  string `json:"gms"`
	} `json:"actions_artifact_names"`

	LogFile           string `json:"log_file"`
	DiscordLogChannel string `json:"discord_log_channel"`
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
	checks := []struct {
		name  string
		value string
	}{
		{"discord_token", c.DiscordToken},
		{"telegram_token", c.TelegramToken},
		{"github_token", c.GitHubToken},
		{"discord_guild_id", c.DiscordGuildID},
		{"github_owner", c.GitHubOwner},
		{"github_repo", c.GitHubRepo},
		{"actions_workflow_file", c.ActionsWorkflowFile},
		{"actions_artifact_names.foss", c.ActionsArtifactNames.Foss},
		{"actions_artifact_names.gms", c.ActionsArtifactNames.GMS},
		{"log_file", c.LogFile},
	}

	for _, ch := range checks {
		if ch.value == "" {
			return fmt.Errorf("required field %q is empty", ch.name)
		}
	}

	if c.TelegramChatID == 0 {
		return fmt.Errorf("required field %q is zero", "telegram_chat_id")
	}

	if len(c.PermaAdminDiscordIDs) == 0 {
		return fmt.Errorf("permaadmin_discord_ids must contain at least one ID")
	}
	if len(c.PermaAdminTelegramIDs) == 0 {
		return fmt.Errorf("permaadmin_telegram_ids must contain at least one ID")
	}

	return nil
}

// GetPermaAdminIDs implements db.PermaAdminProvider.
func (c *Config) GetPermaAdminIDs(platform string) []string {
	switch platform {
	case "discord":
		return c.PermaAdminDiscordIDs
	case "telegram":
		return c.PermaAdminTelegramIDs
	default:
		return nil
	}
}
