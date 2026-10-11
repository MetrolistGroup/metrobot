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

func TestDashboardDisabledByDefault(t *testing.T) {
	cfg := validConfig()
	if err := cfg.validate(); err != nil || cfg.Dashboard.Enabled {
		t.Fatalf("dashboard must be optional: %v", err)
	}
	cfg.Dashboard = DashboardConfig{BaseURL: "invalid", ClientSecret: "unused"}
	if err := cfg.validate(); err != nil {
		t.Fatalf("disabled dashboard validated credentials: %v", err)
	}
}

func TestDashboardValidation(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:8080", "https://admin.example.com"} {
		t.Run(base, func(t *testing.T) {
			d := DashboardConfig{Enabled: true, BaseURL: base, ClientID: "id", ClientSecret: "secret"}
			if err := d.Validate(); err != nil || d.ListenAddress != "127.0.0.1:8080" {
				t.Fatalf("valid local-first config: %+v, %v", d, err)
			}
		})
	}
	for _, base := range []string{
		"", "http://admin.example.com", "http://0.0.0.0:8080", "http://127.0.0.1.evil:8080", "ftp://localhost",
		"http://localhost/", "http://localhost/path", "http://localhost?key=value", "http://localhost?",
		"http://localhost#fragment", "http://localhost#", "http://user:secret@localhost", "//localhost",
		"http://localhost:0", "http://localhost:65536", "http://localhost:", "http://localhost:bad", "https://:443",
	} {
		t.Run(base, func(t *testing.T) {
			d := DashboardConfig{Enabled: true, BaseURL: base, ClientID: "id", ClientSecret: "secret"}
			if err := d.Validate(); err == nil {
				t.Fatalf("unsafe base URL accepted: %q", base)
			}
		})
	}
	for _, address := range []string{"0.0.0.0:8080", ":8080", "admin.example.com:8080", "localhost", "localhost:0", "localhost:65536", "localhost:http"} {
		d := DashboardConfig{Enabled: true, BaseURL: "http://localhost:8080", ClientID: "id", ClientSecret: "secret", ListenAddress: address}
		if err := d.Validate(); err == nil {
			t.Fatalf("unsafe listen address accepted: %q", address)
		}
	}
	for _, d := range []DashboardConfig{
		{Enabled: true, BaseURL: "http://localhost:8080", ClientID: "id"},
		{Enabled: true, BaseURL: "http://localhost:8080", ClientSecret: "secret"},
	} {
		if err := d.Validate(); err == nil {
			t.Fatal("incomplete enabled dashboard accepted")
		}
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
