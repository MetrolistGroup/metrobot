package config

import (
	"strings"
	"testing"
)

func TestValidateRejectsEmptyOpenRouterKey(t *testing.T) {
	config := validConfig()
	config.OpenRouterAPIKeys = []string{"key", "  "}

	err := config.validate()
	if err == nil || !strings.Contains(err.Error(), "openrouter_api_keys[1]") {
		t.Fatalf("validate() error = %v, want empty OpenRouter key error", err)
	}
}

func TestValidateRejectsEmptyFirecrawlKey(t *testing.T) {
	config := validConfig()
	config.FirecrawlAPIKeys = []string{"key", "  "}

	err := config.validate()
	if err == nil || !strings.Contains(err.Error(), "firecrawl_api_keys[1]") {
		t.Fatalf("validate() error = %v, want empty Firecrawl key error", err)
	}
}

func TestOptionalDecisionModel(t *testing.T) {
	cfg := validConfig()
	if cfg.OpenRouterDecisionModel != "" || cfg.validate() != nil {
		t.Fatal("empty decision model must keep existing configuration valid")
	}
	cfg.OpenRouterDecisionModel = "perplexity/pplx-decider-flash-v1.2"
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "requires openrouter_api_keys") {
		t.Fatalf("missing key validation = %v", err)
	}
	cfg.OpenRouterAPIKeys = []string{"key"}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
}

func validConfig() Config {
	config := Config{
		DiscordToken:          "discord",
		TelegramToken:         "telegram",
		GitHubToken:           "github",
		DiscordGuildID:        "guild",
		TelegramChatID:        1,
		PermaAdminDiscordIDs:  []string{"1"},
		PermaAdminTelegramIDs: []string{"1"},
		GitHubOwner:           "owner",
		GitHubRepo:            "repo",
		ActionsWorkflowFile:   "build.yml",
		LogFile:               "bot.log",
	}
	config.ActionsArtifactNames.Foss = "foss"
	config.ActionsArtifactNames.GMS = "gms"
	return config
}
