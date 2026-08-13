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
