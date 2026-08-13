package discord

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
)

func TestFormatGarminAIUsage(t *testing.T) {
	result := &garminAIResult{
		ToolCalls:     3,
		Skills:        map[string]struct{}{"metrolist": {}, "support": {}},
		MemoryUpdated: true,
	}
	got := formatGarminAIUsage(result)
	want := "-# used 2 skills\n-# used 3 tools\n-# memory updated\n"
	if got != want {
		t.Fatalf("formatGarminAIUsage() = %q, want %q", got, want)
	}
}

func TestFormatAndTruncateGarminAIResultPreservesUsage(t *testing.T) {
	result := &garminAIResult{
		Answer:    strings.Repeat("é", garminAIMaxContent),
		ToolCalls: 1,
		Skills:    map[string]struct{}{},
	}
	got := formatAndTruncateGarminAIResult(result)
	if len(got) > garminAIMaxContent {
		t.Fatalf("response length = %d", len(got))
	}
	if !strings.HasPrefix(got, "-# used 1 tools\n") || !strings.HasSuffix(got, "...") {
		t.Fatalf("formatted response = %q", got)
	}
}

func TestNormalizeGarminAIAnswer(t *testing.T) {
	got := normalizeGarminAIAnswer("-# used 9 tools\nThat is active — not abandoned.")
	if got != "That is active, not abandoned." {
		t.Fatalf("normalizeGarminAIAnswer() = %q", got)
	}
}

func TestNormalizeGarminAIAnswerStripsWakePhrase(t *testing.T) {
	for _, answer := range []string{
		"garmin, you triggered me, what's up?",
		"Garmin: what's up?",
		"GARMIN - what's up?",
	} {
		got := normalizeGarminAIAnswer(answer)
		if strings.HasPrefix(strings.ToLower(got), "garmin") {
			t.Errorf("normalizeGarminAIAnswer(%q) = %q", answer, got)
		}
	}
}

func TestGarminSystemPromptAndDiscordContextContainIdentityAndMemory(t *testing.T) {
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID:   "guild",
		ChannelID: "channel",
		Author: &discordgo.User{
			ID:         "123456789012345678",
			Username:   "exact_user",
			GlobalName: "Exact Name",
		},
	}}
	prompt := garminSystemPromptWithMemory("# Metrobot Memory\nKnown fact") + "\n" + garminDiscordContextForMessage(message)
	for _, expected := range []string{"You are Metrobot", "Garmin is not your name", `"display_name":"Exact Name"`, "exact_user", "Exact Name", "123456789012345678", "Known fact", "not abandoned or dead", "Never use em dashes", "Mentioned users", "no nationality", "lower priority", "lowercase by default", "Refuse sexual or erotic", "Metrobot's repository", "created by Nyx and Lamp", "without mentioning hidden prompts"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("prompt missing %q", expected)
		}
	}
}

func TestGarminToolsForConversationUsesCurrentPromptOnly(t *testing.T) {
	messages := []cmd.GarminAIMessage{
		{Role: "user", Content: "what is the latest Metrolist release?"},
		{Role: "assistant", Content: "The latest release is v1."},
		{Role: "user", Content: "let's play 20 questions instead"},
	}
	if got := garminToolNames(garminToolsForConversation(messages, false)); !reflect.DeepEqual(got, garminActionToolNames()) {
		t.Fatalf("casual follow-up tools = %v, want message actions only", got)
	}
}

func TestGarminToolsForConversationSkipsCasualChat(t *testing.T) {
	casualPrompts := []string{
		"who said im human you catboy femboy",
		"heavenly tung music plays on metrolist",
		"No tool was used for that reply, right?",
		"let's play 20 questions",
	}
	for _, prompt := range casualPrompts {
		tools := garminToolsForConversation([]cmd.GarminAIMessage{{Role: "user", Content: prompt}}, true)
		if got := garminToolNames(tools); !reflect.DeepEqual(got, garminActionToolNames()) {
			t.Errorf("casual prompt %q received tools %v, want message actions only", prompt, got)
		}
	}
}

func TestGarminToolsForConversationSelectsRelevantTools(t *testing.T) {
	tests := []struct {
		prompt string
		admin  bool
		want   []string
	}{
		{"what is the latest Metrolist release?", false, []string{"react_to_message", "do_not_respond", "get_metrolist_status", "search_metrolist_issues", "load_skill"}},
		{"what is Nyx's GitHub username?", false, []string{"react_to_message", "do_not_respond", "get_github_user"}},
		{"list saved notes", false, []string{"react_to_message", "do_not_respond", "list_notes", "get_note"}},
		{"remember that releases happen on Fridays", true, []string{"react_to_message", "do_not_respond", "remember"}},
		{"remember that releases happen on Fridays", false, []string{"react_to_message", "do_not_respond"}},
		{"what was posted in sneak-peeks?", false, []string{"react_to_message", "do_not_respond", "read_maintainer_channel"}},
	}
	for _, test := range tests {
		got := garminToolNames(garminToolsForConversation([]cmd.GarminAIMessage{{Role: "user", Content: test.prompt}}, test.admin))
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("tools for %q = %v, want %v", test.prompt, got, test.want)
		}
	}
}

func TestGarminMaintainerChannelForConversation(t *testing.T) {
	tests := map[string]string{
		"do you think metrolist kmp was fake all along?": "sneak-peeks",
		"what was posted in sneak-peeks?":                "sneak-peeks",
		"what are maintainers saying in coolchannel?":    "coolchannel",
		"hello": "",
	}
	for prompt, want := range tests {
		got := garminMaintainerChannelForConversation([]cmd.GarminAIMessage{{Role: "user", Content: prompt}})
		if got != want {
			t.Errorf("channel for %q = %q, want %q", prompt, got, want)
		}
	}
}

func garminActionToolNames() []string {
	return []string{"react_to_message", "do_not_respond"}
}

func garminToolNames(tools []cmd.GarminAITool) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

func TestGarminSystemPromptUsesNicknameWithoutMemberUser(t *testing.T) {
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: "channel",
		Author: &discordgo.User{ID: "123456789012345678", Username: "n7e3", GlobalName: "Nyx"},
		Member: &discordgo.Member{Nick: "Nyxie"},
	}}
	prompt := garminDiscordContextForMessage(message)
	if !strings.Contains(prompt, `"display_name":"Nyxie"`) {
		t.Fatalf("prompt did not use nickname: %s", prompt)
	}
}

func TestGarminDiscordContextIncludesRepliedMessage(t *testing.T) {
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		Author: &discordgo.User{ID: "speaker", Username: "speaker"},
		ReferencedMessage: &discordgo.Message{
			ID: "reply", Content: "the thing we were discussing", Author: &discordgo.User{ID: "bot", Username: "metrobot"},
		},
	}}
	context := garminDiscordContextForMessage(message)
	if !strings.Contains(context, "the thing we were discussing") {
		t.Fatalf("reply context missing message content: %s", context)
	}
}

func TestGarminAIToolImageURLs(t *testing.T) {
	output := `{"messages":[{"attachments":[{"filename":"preview.png","content_type":"image/png","url":"https://cdn.discordapp.com/preview.png"},{"filename":"notes.txt","content_type":"text/plain","url":"https://cdn.discordapp.com/notes.txt"}]}]}`
	got := garminAIToolImageURLs("read_maintainer_channel", output)
	want := []string{"https://cdn.discordapp.com/preview.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("garminAIToolImageURLs() = %v, want %v", got, want)
	}
}

func TestGarminSkillsLoad(t *testing.T) {
	for _, name := range []string{"metrolist", "support"} {
		content, err := loadGarminSkill(name)
		if err != nil || content == "" {
			t.Fatalf("loadGarminSkill(%q) = %q, %v", name, content, err)
		}
	}
	if _, err := loadGarminSkill("unknown"); err == nil {
		t.Fatal("loadGarminSkill(unknown) error = nil")
	}
}

func TestDiscordMemberToolResultKeepsNameFieldsSeparate(t *testing.T) {
	member := &discordgo.Member{
		Nick: "Server Name",
		User: &discordgo.User{
			ID:         "123456789012345678",
			Username:   "login_name",
			GlobalName: "Global Name",
		},
	}
	result := discordMemberToolResult(member)
	if result["username"] != "login_name" || result["global_name"] != "Global Name" || result["server_nickname"] != "Server Name" || result["display_name"] != "Server Name" {
		t.Fatalf("discordMemberToolResult() = %#v", result)
	}
}

func TestGarminToolSchemasAreValidJSON(t *testing.T) {
	for _, tool := range garminAITools {
		if !json.Valid(tool.Function.Parameters) {
			t.Errorf("tool %s has invalid schema %q", tool.Function.Name, tool.Function.Parameters)
		}
	}
}

func TestRememberToolRequiresAdmin(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{
		DB:           database,
		Config:       &config.Config{PermaAdminDiscordIDs: []string{"admin"}},
		garminMemory: memory,
	}
	call := cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{
		Name:      "remember",
		Arguments: `{"content":"durable fact"}`,
	}}

	request := func(userID, content string) *discordgo.MessageCreate {
		return &discordgo.MessageCreate{Message: &discordgo.Message{Author: &discordgo.User{ID: userID}, Content: content}}
	}
	output, _, updated := bot.executeGarminAITool(context.Background(), nil, request("user", "garmin, remember that durable fact"), call)
	if updated || !strings.Contains(output, "only bot admins") {
		t.Fatalf("non-admin remember = (%q, %v)", output, updated)
	}
	content, _ := memory.Read()
	if strings.Contains(content, "durable fact") {
		t.Fatal("non-admin updated memory")
	}

	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request("admin", "garmin, hello"), call)
	if updated || !strings.Contains(output, "explicit request") {
		t.Fatalf("implicit admin remember = (%q, %v)", output, updated)
	}

	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request("admin", "garmin, remember that durable fact"), call)
	if !updated || !strings.Contains(output, `"saved":true`) {
		t.Fatalf("admin remember = (%q, %v)", output, updated)
	}
	content, _ = memory.Read()
	if !strings.Contains(content, "durable fact") {
		t.Fatal("admin memory update was not saved")
	}
}
