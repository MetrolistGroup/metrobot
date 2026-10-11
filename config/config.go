package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type DashboardConfig struct {
	Enabled       bool   `json:"enabled"`
	ListenAddress string `json:"listen_address"`
	BaseURL       string `json:"base_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
}

// Validate keeps HTTP cookies restricted to a loopback-only local setup.
func (d *DashboardConfig) Validate() error {
	if !d.Enabled {
		return nil
	}
	if strings.TrimSpace(d.ClientID) == "" || strings.TrimSpace(d.ClientSecret) == "" {
		return fmt.Errorf("dashboard requires client_id and client_secret")
	}
	u, err := url.Parse(d.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(d.BaseURL, "#") || u.Opaque != "" {
		return fmt.Errorf("dashboard base_url must be an origin without path, query, fragment or userinfo")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return fmt.Errorf("dashboard base_url requires HTTPS except on loopback")
	}
	if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("dashboard base_url has an invalid port")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("dashboard base_url has an invalid port")
		}
	}
	if d.ListenAddress == "" {
		d.ListenAddress = "127.0.0.1:8080"
		if u.Scheme == "http" {
			port := u.Port()
			if port == "" {
				port = "80"
			}
			d.ListenAddress = net.JoinHostPort(u.Hostname(), port)
		}
	}
	host, port, err := net.SplitHostPort(d.ListenAddress)
	if err != nil {
		return fmt.Errorf("dashboard listen_address must be host:port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("dashboard listen_address has an invalid port")
	}
	if u.Scheme == "http" && !isLoopback(host) {
		return fmt.Errorf("HTTP dashboard listen_address must be loopback")
	}
	return nil
}

func isLoopback(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

type Config struct {
	Dashboard         DashboardConfig `json:"dashboard"`
	DiscordToken      string          `json:"discord_token"`
	TelegramToken     string          `json:"telegram_token"`
	GitHubToken       string          `json:"github_token"`
	OpenRouterAPIKeys []string        `json:"openrouter_api_keys"`
	OpenRouterModel   string          `json:"openrouter_model"`
	FirecrawlAPIKeys  []string        `json:"firecrawl_api_keys"`
	GarminMemoryFile  string          `json:"garmin_memory_file"`

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

	// Starboard configuration
	StarboardChannelID string `json:"starboard_channel_id"`
	StarboardThreshold int    `json:"starboard_threshold"`
	StarboardEmoji     string `json:"starboard_emoji"`
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

	return c.Dashboard.Validate()
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
