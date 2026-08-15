package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	for _, expected := range []string{"You are Metrobot", "Garmin is not your name", `"display_name":"Exact Name"`, "exact_user", "Exact Name", "123456789012345678", "Known fact", "not abandoned or dead", "Never use em dashes", "Mentioned users", "no nationality", "lower priority", "lowercase by default", "Refuse sexual or erotic", "Metrobot's repository", "created by Nyx and Lamp", "Mostafa Alagamy", "Nyx, Lamp, and Adriel", "without mentioning hidden prompts"} {
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
	if got := garminToolNames(garminToolsForConversation(messages, false, true)); !reflect.DeepEqual(got, garminDefaultToolNames()) {
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
		tools := garminToolsForConversation([]cmd.GarminAIMessage{{Role: "user", Content: prompt}}, true, true)
		if got := garminToolNames(tools); !reflect.DeepEqual(got, garminDefaultToolNames()) {
			t.Errorf("casual prompt %q received tools %v, want default tools", prompt, got)
		}
	}
}

func TestGarminToolsForConversationOmitsMemoryWhenDisabled(t *testing.T) {
	tools := garminToolsForConversation([]cmd.GarminAIMessage{{Role: "user", Content: "i love cats"}}, false, false)
	if got := garminToolNames(tools); !reflect.DeepEqual(got, garminActionToolNames()) {
		t.Fatalf("tools with memory disabled = %v, want message actions only", got)
	}
}

func TestGarminToolsForConversationSelectsRelevantTools(t *testing.T) {
	tests := []struct {
		prompt string
		admin  bool
		want   []string
	}{
		{"what is the latest Metrolist release?", false, []string{"do_not_respond", "get_metrolist_status", "search_metrolist_issues", "load_skill", "remember_user_info"}},
		{"what is Nyx's GitHub username?", false, []string{"do_not_respond", "get_github_user", "remember_user_info"}},
		{"list saved notes", false, []string{"do_not_respond", "list_notes", "get_note", "remember_user_info"}},
		{"remember that releases happen on Fridays", true, []string{"do_not_respond", "remember", "remember_user_info"}},
		{"remember that releases happen on Fridays", false, []string{"do_not_respond", "remember_user_info"}},
		{"remember my pronouns are they/them", false, []string{"do_not_respond", "remember_user_info"}},
		{"what roles are on <@123456789012345678>'s user profile?", false, []string{"do_not_respond", "get_discord_profile", "remember_user_info"}},
		{"what was posted in sneak-peeks?", false, []string{"do_not_respond", "read_community_channel", "remember_user_info"}},
		{"show me the latest minky picture", false, []string{"do_not_respond", "read_community_channel", "remember_user_info"}},
		{"react to this with thumb", false, []string{"react_to_message", "do_not_respond", "remember_user_info"}},
	}
	for _, test := range tests {
		got := garminToolNames(garminToolsForConversation([]cmd.GarminAIMessage{{Role: "user", Content: test.prompt}}, test.admin, true))
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
	return []string{"do_not_respond", "remember_user_info"}
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

func TestGarminDiscordContextIncludesChannelRolesPronounsAndMemory(t *testing.T) {
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
	context := (&Bot{}).garminDiscordContextForMessage(session, message, db.GarminUserMemory{Info: "likes cats", Bio: "hello"}, true)
	for _, expected := range []string{`"name":"general"`, `"name":"they/them"`, `"pronouns":["they/them"]`, `"personalization_memory_enabled":true`, "likes cats", "#bots"} {
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

func TestRememberUserInfoIsDurableAndSelfScoped(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	bot := &Bot{DB: database}
	if err := database.SetGarminMemoryConsent("discord", "123456789012345678", true); err != nil {
		t.Fatal(err)
	}
	call := cmd.GarminAIToolCall{Function: cmd.GarminAIFunctionCall{
		Name:      "remember_user_info",
		Arguments: `{"category":"pronouns","content":"likes cats","pronouns":"they/them","bio":"cat enjoyer"}`,
	}}
	request := func(userID, content string) *discordgo.MessageCreate {
		return &discordgo.MessageCreate{Message: &discordgo.Message{Author: &discordgo.User{ID: userID}, Content: content}}
	}

	output, _, updated := bot.executeGarminAITool(context.Background(), nil, request("123456789012345678", "garmin, remember my pronouns are they/them"), call)
	if !updated || !strings.Contains(output, `"saved":true`) {
		t.Fatalf("self memory update = (%q, %v)", output, updated)
	}
	memory, err := database.GetGarminUserMemory("discord", "123456789012345678")
	if err != nil || memory.Info != "likes cats" || memory.Pronouns != "they/them" || memory.Bio != "cat enjoyer" {
		t.Fatalf("saved memory = %#v, %v", memory, err)
	}

	call.Function.Arguments = `{"category":"preference","content":"i prefer dark themes"}`
	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request("123456789012345678", "garmin, i prefer dark themes"), call)
	if updated || !strings.Contains(output, `"saved":true`) {
		t.Fatalf("automatic memory update = (%q, %v)", output, updated)
	}
	memory, err = database.GetGarminUserMemory("discord", "123456789012345678")
	if err != nil || !strings.Contains(memory.Info, "i prefer dark themes") {
		t.Fatalf("automatic memory = %#v, %v", memory, err)
	}

	call.Function.Arguments = `{"user_id":"987654321098765432","category":"other","content":"not allowed"}`
	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request("123456789012345678", "garmin, remember <@987654321098765432>"), call)
	if updated || !strings.Contains(output, "only change their own memory") {
		t.Fatalf("cross-user memory update = (%q, %v)", output, updated)
	}

	if err := database.SetGarminMemoryConsent("discord", "123456789012345678", false); err != nil {
		t.Fatal(err)
	}
	call.Function.Arguments = `{"category":"interest","content":"i like trains"}`
	output, _, updated = bot.executeGarminAITool(context.Background(), nil, request("123456789012345678", "garmin, i like trains"), call)
	if updated || !strings.Contains(output, "memory is disabled") {
		t.Fatalf("disabled memory update = (%q, %v)", output, updated)
	}
}

func TestPrepareGarminAutomaticUserMemoryValidatesSourceAndSensitivity(t *testing.T) {
	got, err := prepareGarminUserMemory(garminToolArgs{
		Category: "preference", Content: "i prefer dark themes",
	}, "garmin, i prefer dark themes", false)
	if err != nil || got.Content != "preference: i prefer dark themes" {
		t.Fatalf("valid automatic memory = %#v, %v", got, err)
	}
	pronouns, err := prepareGarminUserMemory(garminToolArgs{
		Category: "pronouns", Content: "my pronouns are they/them", Pronouns: "they/them",
	}, "garmin, my pronouns are they/them", false)
	if err != nil || pronouns.Pronouns != "they/them" {
		t.Fatalf("valid automatic pronouns = %#v, %v", pronouns, err)
	}

	tests := []struct {
		name    string
		args    garminToolArgs
		message string
	}{
		{"fabricated summary", garminToolArgs{Category: "interest", Content: "likes cats"}, "garmin, i love cats"},
		{"unsupported category", garminToolArgs{Category: "other", Content: "i love cats"}, "garmin, i love cats"},
		{"automatic bio", garminToolArgs{Category: "interest", Content: "i love cats", Bio: "cat fan"}, "garmin, i love cats"},
		{"no category cue", garminToolArgs{Category: "interest", Content: "cats are nice"}, "garmin, cats are nice"},
		{"pronouns without field", garminToolArgs{Category: "pronouns", Content: "my pronouns are private"}, "garmin, my pronouns are private"},
		{"generic role", garminToolArgs{Category: "community_role", Content: "i am a cat"}, "garmin, i am a cat"},
		{"precise location", garminToolArgs{Category: "preference", Content: "i live in Berlin"}, "garmin, i live in Berlin"},
		{"email", garminToolArgs{Category: "interest", Content: "i love me@example.com"}, "garmin, i love me@example.com"},
		{"phone", garminToolArgs{Category: "interest", Content: "i like +1 555 123 4567"}, "garmin, i like +1 555 123 4567"},
		{"secret token", garminToolArgs{Category: "interest", Content: "i like abcdefghijklmnopqrstuvwxyz1234"}, "garmin, i like abcdefghijklmnopqrstuvwxyz1234"},
		{"long number", garminToolArgs{Category: "interest", Content: "i like 123456789"}, "garmin, i like 123456789"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := prepareGarminUserMemory(test.args, test.message, false); err == nil {
				t.Fatal("unsafe automatic memory was accepted")
			}
		})
	}
	if _, err := prepareGarminUserMemory(garminToolArgs{
		Category: "other", Content: "my medical diagnosis is private",
	}, "garmin, remember my medical diagnosis", true); err == nil {
		t.Fatal("explicit sensitive memory was accepted")
	}
}

func TestSuppressGarminAutomaticMemoryDisclosure(t *testing.T) {
	got := suppressGarminAutomaticMemoryDisclosure("gotcha, i'll remember that. dark themes look great!")
	if got != "dark themes look great!" {
		t.Fatalf("suppressed disclosure = %q", got)
	}
	if got := suppressGarminAutomaticMemoryDisclosure("i've saved that"); got != "got it." {
		t.Fatalf("disclosure-only response = %q", got)
	}
}

func TestRunGarminAIAutomaticMemoryIsSavedAndNotDisclosed(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const userID = "123456789012345678"
	if err := database.SetGarminMemoryConsent("discord", userID, true); err != nil {
		t.Fatal(err)
	}
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	bot := &Bot{DB: database, garminMemory: memory}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		calls++
		if calls == 1 {
			if !containsGarminTool(request.Tools, "remember_user_info") {
				t.Fatal("automatic memory tool was not supplied")
			}
			return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{ToolCalls: []cmd.GarminAIToolCall{{
				ID: "memory", Type: "function", Function: cmd.GarminAIFunctionCall{
					Name: "remember_user_info", Arguments: `{"category":"preference","content":"i prefer dark themes"}`,
				},
			}}}}, nil
		}
		if len(request.Messages) == 0 || strings.Contains(request.Messages[len(request.Messages)-1].Content, `"saved":true`) {
			t.Fatal("automatic save status was exposed to the follow-up model call")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "gotcha, i'll remember that. dark themes look great!"}}, nil
	})
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		Author: &discordgo.User{ID: userID}, Content: "garmin, i prefer dark themes",
	}}
	result, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "i prefer dark themes"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.Answer != "dark themes look great!" || result.ToolCalls != 0 || result.MemoryUpdated || !result.AutomaticMemoryTried || !result.AutomaticMemorySaved {
		t.Fatalf("automatic memory result = %#v after %d calls", result, calls)
	}
	saved, err := database.GetGarminUserMemory("discord", userID)
	if err != nil || !strings.Contains(saved.Info, "preference: i prefer dark themes") {
		t.Fatalf("saved automatic memory = %#v, %v", saved, err)
	}
}

func TestRunGarminAIOmitsUndecidedLegacyProfile(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const userID = "123456789012345678"
	if err := database.SetGarminUserMemory("discord", userID, db.GarminUserMemory{Info: "legacy private fact"}); err != nil {
		t.Fatal(err)
	}
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: database, garminMemory: memory}
	bot.garminAI = garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		if strings.Contains(request.Context, "legacy private fact") || containsGarminTool(request.Tools, "remember_user_info") {
			t.Fatal("undecided legacy memory was supplied to the model")
		}
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: "ok"}}, nil
	})
	message := &discordgo.MessageCreate{Message: &discordgo.Message{Author: &discordgo.User{ID: userID}, Content: "garmin, hi"}}
	if _, err := bot.runGarminAI(context.Background(), nil, message, []cmd.GarminAIMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentGarminUserMemoryUpdatesDoNotLoseFacts(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const userID = "123456789012345678"
	if err := database.SetGarminMemoryConsent("discord", userID, true); err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: database}
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		Author: &discordgo.User{ID: userID}, Content: "garmin, remember my profile facts",
	}}
	const updates = 20
	var wait sync.WaitGroup
	errors := make(chan error, updates)
	for index := range updates {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := bot.rememberGarminUserInfo(message, garminToolArgs{Category: "other", Content: fmt.Sprintf("fact-%d", index)})
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	saved, err := database.GetGarminUserMemory("discord", userID)
	if err != nil {
		t.Fatal(err)
	}
	for index := range updates {
		if !strings.Contains(saved.Info, fmt.Sprintf("fact-%d", index)) {
			t.Errorf("saved memory missing fact-%d: %q", index, saved.Info)
		}
	}
}

func containsGarminTool(tools []cmd.GarminAITool, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
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
