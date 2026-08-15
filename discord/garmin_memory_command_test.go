package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

func TestHandleGarminMemoryUsersIsAdminOnlyAndAttachesProfiles(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AddAdmin("discord", "admin", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetGarminUserMemory("discord", "123456789012345678", db.GarminUserMemory{Info: "likes cats"}); err != nil {
		t.Fatal(err)
	}

	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading interaction response: %v", err)
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	bot := &Bot{DB: database, Config: &config.Config{}, Logger: zap.NewNop(), garminMemory: memory}
	options := []*discordgo.ApplicationCommandInteractionDataOption{{
		Type: discordgo.ApplicationCommandOptionSubCommand, Name: "users",
	}}
	interaction := func(id string) *discordgo.InteractionCreate {
		return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{ID: id, AppID: "app", Token: "token"}}
	}

	bot.handleGarminMemory(session, interaction("non-admin"), options, "user")
	bot.handleGarminMemory(session, interaction("admin"), options, "admin")
	if len(bodies) != 2 {
		t.Fatalf("interaction responses = %d, want 2", len(bodies))
	}
	if !strings.Contains(string(bodies[0]), "Only admins") {
		t.Fatalf("non-admin response = %s", bodies[0])
	}
	adminResponse := string(bodies[1])
	for _, expected := range []string{"garmin-user-memories.md", "123456789012345678", "likes cats"} {
		if !strings.Contains(adminResponse, expected) {
			t.Errorf("admin response missing %q", expected)
		}
	}
}

func TestFormatGarminUserMemoryExportSplitsLargeOutput(t *testing.T) {
	const entries = 1900
	memories := make([]db.GarminUserMemoryEntry, entries)
	info := strings.Repeat("x", 4000)
	for index := range memories {
		memories[index] = db.GarminUserMemoryEntry{
			UserID:           fmt.Sprintf("%018d", index),
			GarminUserMemory: db.GarminUserMemory{Info: info},
		}
	}
	chunks, err := formatGarminUserMemoryExport(memories)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("large export chunks = %d, want at least 2", len(chunks))
	}
	for index, chunk := range chunks {
		if len(chunk) > 7*1024*1024 {
			t.Errorf("chunk %d size = %d", index, len(chunk))
		}
	}
	legacyChunks, err := formatGarminUserMemoryExport([]db.GarminUserMemoryEntry{{
		UserID: "legacy", GarminUserMemory: db.GarminUserMemory{Info: strings.Repeat("é", 4*1024*1024)},
	}})
	if err != nil || len(legacyChunks) != 2 {
		t.Fatalf("oversized legacy entry chunks = %d, %v", len(legacyChunks), err)
	}
	for index, chunk := range legacyChunks {
		if len(chunk) > 7*1024*1024 || !utf8.ValidString(chunk) {
			t.Errorf("legacy chunk %d is invalid or oversized", index)
		}
	}
}

func TestRegisterCommandsIncludesMemoryPrivacyActions(t *testing.T) {
	var commands []*discordgo.ApplicationCommand
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&commands); err != nil {
			t.Errorf("decoding registered commands: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = server.Client()
	session.Client.Transport = rewriteDiscordTransport{base: session.Client.Transport, target: server.URL}
	session.State.User = &discordgo.User{ID: "bot"}
	bot := &Bot{Session: session, Config: &config.Config{DiscordGuildID: "guild"}, Logger: zap.NewNop()}
	if err := bot.registerCommands(); err != nil {
		t.Fatal(err)
	}
	var memoryCommand *discordgo.ApplicationCommand
	for _, command := range commands {
		if command.Name == "memory" {
			memoryCommand = command
			break
		}
	}
	if memoryCommand == nil {
		t.Fatal("memory command was not registered")
	}
	options := make(map[string]*discordgo.ApplicationCommandOption)
	for _, option := range memoryCommand.Options {
		options[option.Name] = option
	}
	for _, name := range []string{"users", "personalization"} {
		if options[name] == nil || options[name].Type != discordgo.ApplicationCommandOptionSubCommand {
			t.Errorf("memory subcommand %q = %#v", name, options[name])
		}
	}
	personalization := options["personalization"]
	if personalization != nil && (len(personalization.Options) != 1 || personalization.Options[0].Name != "enabled" || personalization.Options[0].Type != discordgo.ApplicationCommandOptionBoolean || !personalization.Options[0].Required) {
		t.Errorf("personalization options = %#v", personalization.Options)
	}
}
