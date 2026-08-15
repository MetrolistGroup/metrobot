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

type garminAITestFunc func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error)

func (f garminAITestFunc) Complete(ctx context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
	return f(ctx, request)
}

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

func TestNormalizeGarminAIAnswerStripsInternalMarkupAndMemoryOffers(t *testing.T) {
	tests := map[string]string{
		"<|mask_start|>\n\nbased on what i know, your favorite artist is camellia.\n\ndo you want me to save that as a permanent preference?\n\n<|mask_end|>": "based on what i know, your favorite artist is camellia.",
		"<function=remember_user_info>\n<parameter=category>\ninterest\n</parameter>\n<parameter=content>\ni like beans on toast\n</parameter>\n</function>":  "got it.",
		"ok\n\n<:hm:1439319659106013294>":           "ok",
		"ok :hm:":                                   "ok",
		"ok :unknown_custom_emoji:":                 "ok",
		"useful. i can store that in your profile.": "useful.",
		"i can save that for future replies.":       "got it.",
		"i could save that for later.":              "got it.",
		"would you like this saved?":                "got it.",
		"let me save that for later.":               "got it.",
		"the app can save battery.":                 "the app can save battery.",
	}
	for input, want := range tests {
		if got := normalizeGarminAIAnswer(input); got != want {
			t.Errorf("normalizeGarminAIAnswer(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRunGarminAIAmbientModeCanReplyReactOrStaySilent(t *testing.T) {
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{garminMemory: memory, garminAIContexts: make(map[string]garminAIContext), garminAIUserContexts: make(map[string]garminAIContext)}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if !strings.Contains(request.Context, "active Metrobot conversation") {
			t.Fatal("ambient decision instruction was not supplied")
		}
		if got, want := garminToolNames(request.Tools), []string{"react_to_message", "do_not_respond"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ambient request tools = %v, want %v", got, want)
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{
			ID: "silent", Type: "function", Function: cmd.GarminAIFunctionCall{Name: "do_not_respond", Arguments: `{}`},
		}}}}, nil
	})
	message := &discordgo.MessageCreate{Message: &discordgo.Message{GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "user"}, Content: "maybe never mind"}}
	bot.rememberGarminAIContext("previous", message, []cmd.GarminAIMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hey"}})
	ambientToken := bot.tryBeginGarminAIAmbient(message)
	defer bot.endGarminAIAmbient(message, ambientToken)
	result, err := bot.runGarminAIWithMode(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "maybe never mind"}}, true, ambientToken)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Silent {
		t.Fatalf("ambient do_not_respond result = %#v", result)
	}
}

func TestGarminSystemPromptAndDiscordContextContainIdentityAndGlobalMemory(t *testing.T) {
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
	for _, expected := range []string{"You are Metrobot", "Garmin is not your name", `"display_name":"exact_user"`, "exact_user", "123456789012345678", "Known fact", "not abandoned or dead", "Never use em dashes", "Mentioned users", "no nationality", "lower priority", "lowercase by default", "Refuse sexual or erotic", "Metrobot's repository", "created by Nyx and Lamp", "Mostafa Alagamy", "Nyx, Lamp, and Adriel", "without mentioning hidden prompts"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("prompt missing %q", expected)
		}
	}
	for _, removed := range []string{"personalization memory", "current_user_memory", "remember_user_info"} {
		if strings.Contains(prompt, removed) {
			t.Errorf("prompt contains removed per-user memory feature %q", removed)
		}
	}
	if strings.Contains(prompt, "Exact Name") {
		t.Fatal("global display name leaked into Discord context")
	}
}

func TestGarminToolsForConversationUsesCurrentPromptOnly(t *testing.T) {
	messages := []cmd.GarminAIMessage{
		{Role: "user", Content: "what is the latest Metrolist release?"},
		{Role: "assistant", Content: "The latest release is v1."},
		{Role: "user", Content: "let's play 20 questions instead"},
	}
	if got := garminToolNames(garminToolsForConversation(messages, false, false)); !reflect.DeepEqual(got, garminDefaultToolNames()) {
		t.Fatalf("casual follow-up tools = %v, want default tools", got)
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
		tools := garminToolsForConversation([]cmd.GarminAIMessage{{Role: "user", Content: prompt}}, true, false)
		if got := garminToolNames(tools); !reflect.DeepEqual(got, garminDefaultToolNames()) {
			t.Errorf("casual prompt %q received tools %v, want default tools", prompt, got)
		}
	}
}

func TestGarminToolsForConversationSelectsRelevantTools(t *testing.T) {
	tests := []struct {
		prompt string
		admin  bool
		want   []string
	}{
		{"what is the latest Metrolist release?", false, []string{"do_not_respond", "get_metrolist_status", "search_metrolist_issues", "load_skill"}},
		{"what is Nyx's GitHub username?", false, []string{"do_not_respond", "get_github_user"}},
		{"search GitHub repos for a Kotlin music client", false, []string{"do_not_respond", "search_github_repositories", "get_github_repository"}},
		{"show details for the facebook/react repository", false, []string{"do_not_respond", "search_github_repositories", "get_github_repository"}},
		{"what is https://github.com/facebook/react?", false, []string{"do_not_respond", "search_github_repositories", "get_github_repository"}},
		{"list saved notes", false, []string{"do_not_respond", "list_notes", "get_note"}},
		{"remember that releases happen on Fridays", true, []string{"do_not_respond", "remember"}},
		{"remember that releases happen on Fridays", false, []string{"do_not_respond"}},
		{"remember my pronouns are they/them", false, []string{"do_not_respond"}},
		{"what roles are on <@123456789012345678>'s user profile?", false, []string{"do_not_respond", "get_discord_profile"}},
		{"what was posted in sneak-peeks?", false, []string{"do_not_respond", "read_community_channel"}},
		{"show me the latest minky picture", false, []string{"do_not_respond", "read_community_channel"}},
		{"react to this with thumb", false, []string{"react_to_message", "do_not_respond"}},
	}
	for _, test := range tests {
		got := garminToolNames(garminToolsForConversation([]cmd.GarminAIMessage{{Role: "user", Content: test.prompt}}, test.admin, false))
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("tools for %q = %v, want %v", test.prompt, got, test.want)
		}
	}
}

func TestGarminReadableChannelForConversation(t *testing.T) {
	tests := map[string]string{
		"do you think metrolist kmp was fake all along?": "sneak-peeks",
		"what was posted in sneak-peeks?":                "sneak-peeks",
		"what are maintainers saying in coolchannel?":    "coolchannel",
		"what is the latest poll about app design?":      "polls",
		"show me a picture of minky":                     "minky",
		"hello":                                          "",
	}
	for prompt, want := range tests {
		got := garminReadableChannelForConversation([]cmd.GarminAIMessage{{Role: "user", Content: prompt}})
		if got != want {
			t.Errorf("channel for %q = %q, want %q", prompt, got, want)
		}
	}
}

func garminActionToolNames() []string {
	return []string{"do_not_respond"}
}

func garminDefaultToolNames() []string {
	return []string{"do_not_respond"}
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

func TestGarminDiscordContextIncludesChannelRolesAndPronouns(t *testing.T) {
	state := discordgo.NewState()
	if err := state.GuildAdd(&discordgo.Guild{
		ID:    "guild",
		Roles: []*discordgo.Role{{ID: "role-pronouns", Name: "they/them"}, {ID: "role-team", Name: "team"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.ChannelAdd(&discordgo.Channel{
		ID: garminGeneralID, GuildID: "guild", Name: "general", Topic: "community chat",
	}); err != nil {
		t.Fatal(err)
	}
	session := &discordgo.Session{State: state}
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "guild", ChannelID: garminGeneralID,
		Author: &discordgo.User{ID: "123456789012345678", Username: "speaker"},
		Member: &discordgo.Member{Roles: []string{"role-pronouns", "role-team"}},
	}}
	context := (&Bot{}).garminDiscordContextForMessage(session, message)
	for _, expected := range []string{`"name":"general"`, `"name":"they/them"`, `"pronouns":["they/them"]`, "#bots"} {
		if !strings.Contains(context, expected) {
			t.Errorf("context missing %q: %s", expected, context)
		}
	}
}

func TestGarminAIToolImageURLs(t *testing.T) {
	output := `{"messages":[{"attachments":[{"filename":"preview.png","content_type":"image/png","url":"https://cdn.discordapp.com/preview.png"},{"filename":"notes.txt","content_type":"text/plain","url":"https://cdn.discordapp.com/notes.txt"}]}]}`
	got := garminAIToolImageURLs("read_community_channel", output)
	want := []string{"https://cdn.discordapp.com/preview.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("garminAIToolImageURLs() = %v, want %v", got, want)
	}
}

func TestGarminAISilentAnswer(t *testing.T) {
	for _, answer := range []string{"do_not_respond", "`do_not_respond`", "do not respond"} {
		if !garminAISilentAnswer(answer) {
			t.Errorf("garminAISilentAnswer(%q) = false", answer)
		}
	}
	if garminAISilentAnswer("i do not respond to spam") {
		t.Fatal("ordinary sentence was treated as silence sentinel")
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
	if result["username"] != "login_name" || result["server_nickname"] != "Server Name" || result["display_name"] != "Server Name" {
		t.Fatalf("discordMemberToolResult() = %#v", result)
	}
	if _, present := result["global_name"]; present {
		t.Fatalf("discordMemberToolResult() exposed global name: %#v", result)
	}
}

func TestGarminToolSchemasAreValidJSON(t *testing.T) {
	for _, tool := range garminAITools {
		if !json.Valid(tool.Function.Parameters) {
			t.Errorf("tool %s has invalid schema %q", tool.Function.Name, tool.Function.Parameters)
		}
		if tool.Function.Name == "remember_user_info" || tool.Function.Name == "forget_user_info" {
			t.Errorf("removed per-user memory tool %q is still registered", tool.Function.Name)
		}
	}
}

func TestParseGarminTextReactions(t *testing.T) {
	content := "react_to_message message_id=1538281518466867410 reaction=\"👍\"\n" +
		"react_to_message message_id=1538281518466867410 reaction=\"❤️\"\n" +
		"react_to_message emoji=\"speed\""
	if got, want := parseGarminTextReactions(content), []string{"👍", "❤️", "speed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("text reactions = %v, want %v", got, want)
	}
}

func TestRememberToolRequiresOwner(t *testing.T) {
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
	if updated || !strings.Contains(output, "only Nyx and Lamp") {
		t.Fatalf("non-owner remember = (%q, %v)", output, updated)
	}
	content, _ := memory.Read()
	if strings.Contains(content, "durable fact") {
		t.Fatal("non-admin updated memory")
	}

	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request(garminNyxID, "garmin, hello"), call)
	if updated || !strings.Contains(output, "explicit request") {
		t.Fatalf("implicit admin remember = (%q, %v)", output, updated)
	}

	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request(garminNyxID, "garmin, remember that durable fact"), call)
	if !updated || !strings.Contains(output, `"saved":true`) {
		t.Fatalf("admin remember = (%q, %v)", output, updated)
	}
	content, _ = memory.Read()
	if !strings.Contains(content, "durable fact") {
		t.Fatal("admin memory update was not saved")
	}
}

func TestGarminPronounsFromRoles(t *testing.T) {
	roles := []map[string]string{{"id": "1", "name": "she/her"}, {"id": "2", "name": "maintainer"}}
	if got := garminPronounsFromRoles(roles); !reflect.DeepEqual(got, []string{"she/her"}) {
		t.Fatalf("garminPronounsFromRoles() = %v", got)
	}
}

func TestGarminChannelDescriptionsUseResolvedIDs(t *testing.T) {
	for channelID, expected := range map[string]string{
		garminGeneralID:    "#bots",
		garminBotsID:       "preferred channel",
		garminPollsID:      "polls",
		garminMinkyID:      "Minky",
		garminAppSupportID: "support notes",
	} {
		if got := garminChannelDescription(channelID); !strings.Contains(got, expected) {
			t.Errorf("garminChannelDescription(%q) = %q, want substring %q", channelID, got, expected)
		}
	}
}
