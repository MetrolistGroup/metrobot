package discord

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/bwmarrin/discordgo"
)

const (
	garminAIMaxToolRounds  = 4
	garminAIToolResultSize = 12 * 1024
)

//go:embed garmin_skills/*.md
var garminSkillFiles embed.FS

type garminAIResult struct {
	Answer        string
	Conversation  []cmd.GarminAIMessage
	ToolCalls     int
	Skills        map[string]struct{}
	MemoryUpdated bool
}

type garminToolArgs struct {
	Query    string `json:"query"`
	Name     string `json:"name"`
	Username string `json:"username"`
	UserID   string `json:"user_id"`
	Content  string `json:"content"`
}

func (b *Bot) runGarminAI(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) (*garminAIResult, error) {
	memory, err := b.garminMemory.Read()
	if err != nil {
		return nil, err
	}

	systemPrompt := garminSystemPromptForMessage(s, m, memory)
	conversation := copyGarminAIMessages(messages)
	result := &garminAIResult{Skills: make(map[string]struct{})}
	for range garminAIMaxToolRounds {
		completion, err := b.garminAI.Complete(ctx, cmd.GarminAIRequest{
			SystemPrompt: systemPrompt,
			Messages:     conversation,
			Tools:        garminAITools,
		})
		if err != nil {
			return nil, err
		}

		assistantMessage := completion.Message
		assistantMessage.Role = "assistant"
		conversation = append(conversation, assistantMessage)
		if len(assistantMessage.ToolCalls) == 0 {
			answer := normalizeGarminAIAnswer(assistantMessage.Content)
			if answer == "" {
				return nil, fmt.Errorf("AI provider returned no final response")
			}
			result.Answer = answer
			result.Conversation = conversation
			return result, nil
		}

		for _, toolCall := range assistantMessage.ToolCalls {
			result.ToolCalls++
			output, skill, memoryUpdated := b.executeGarminAITool(ctx, s, m, toolCall)
			if skill != "" {
				result.Skills[skill] = struct{}{}
			}
			result.MemoryUpdated = result.MemoryUpdated || memoryUpdated
			conversation = append(conversation, cmd.GarminAIMessage{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Content:    truncateGarminAIToolResult(output),
			})
		}
	}
	return nil, fmt.Errorf("AI exceeded the tool-call limit")
}

func (b *Bot) executeGarminAITool(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, call cmd.GarminAIToolCall) (output, skill string, memoryUpdated bool) {
	var args garminToolArgs
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return toolError(fmt.Errorf("invalid arguments: %w", err)), "", false
	}

	var err error
	switch call.Function.Name {
	case "get_metrolist_status":
		output, err = b.garminGitHub.ProjectStatus(ctx)
	case "search_metrolist_issues":
		output, err = b.garminGitHub.SearchIssues(ctx, args.Query)
	case "get_github_user":
		output, err = b.garminGitHub.User(ctx, args.Username)
	case "list_notes":
		var names []string
		names, err = b.DB.ListNotes()
		if err == nil {
			output = mustJSON(map[string]any{"notes": names})
		}
	case "get_note":
		output, err = b.Notes.GetNote(args.Name)
	case "get_discord_member":
		output, err = b.getGarminDiscordMember(s, args.UserID)
	case "search_discord_members":
		output, err = b.searchGarminDiscordMembers(s, args.Query)
	case "load_skill":
		output, err = loadGarminSkill(args.Name)
		if err == nil {
			skill = strings.ToLower(strings.TrimSpace(args.Name))
		}
	case "remember":
		if !b.DB.IsAdmin("discord", m.Author.ID, b.Config) {
			return toolError(fmt.Errorf("only bot admins can update memory")), "", false
		}
		err = b.garminMemory.Append(args.Content)
		if err == nil {
			output = `{"saved":true}`
			memoryUpdated = true
		}
	default:
		err = fmt.Errorf("unknown tool %q", call.Function.Name)
	}
	if err != nil {
		return toolError(err), "", false
	}
	return output, skill, memoryUpdated
}

func garminSystemPromptForMessage(_ *discordgo.Session, m *discordgo.MessageCreate, memory string) string {
	author := map[string]any{
		"id":          m.Author.ID,
		"username":    m.Author.Username,
		"global_name": m.Author.GlobalName,
	}
	if m.Member != nil {
		author["server_nickname"] = m.Member.Nick
		author["display_name"] = discordMemberDisplayName(m.Member)
	}
	context := map[string]any{
		"current_user": author,
		"channel_id":   m.ChannelID,
		"guild_id":     m.GuildID,
	}
	if len(m.Mentions) > 0 {
		mentions := make([]map[string]any, 0, len(m.Mentions))
		for _, user := range m.Mentions {
			mentions = append(mentions, map[string]any{
				"id":          user.ID,
				"username":    user.Username,
				"global_name": user.GlobalName,
			})
		}
		context["mentioned_users"] = mentions
	}
	if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
		context["replied_to_user"] = map[string]any{
			"id":          m.ReferencedMessage.Author.ID,
			"username":    m.ReferencedMessage.Author.Username,
			"global_name": m.ReferencedMessage.Author.GlobalName,
		}
	}
	return cmd.GarminSystemPrompt() + "\n\nCurrent Discord context (authoritative JSON):\n" + mustJSON(context) + "\n\nPersistent memory (admin-managed Markdown):\n" + memory
}

func (b *Bot) getGarminDiscordMember(s *discordgo.Session, userID string) (string, error) {
	userID = normalizeDiscordUserID(userID)
	if userID == "" {
		return "", fmt.Errorf("Discord user ID is required")
	}
	member, err := s.GuildMember(b.Config.DiscordGuildID, userID)
	if err != nil {
		return "", fmt.Errorf("fetching Discord member: %w", err)
	}
	return mustJSON(discordMemberToolResult(member)), nil
}

func (b *Bot) searchGarminDiscordMembers(s *discordgo.Session, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("member search query is required")
	}
	if userID := normalizeDiscordUserID(query); userID != "" {
		return b.getGarminDiscordMember(s, userID)
	}

	members, err := s.GuildMembersSearch(b.Config.DiscordGuildID, query, 10)
	if err != nil {
		return "", fmt.Errorf("searching Discord members: %w", err)
	}
	results := make([]map[string]any, 0, len(members))
	for _, member := range members {
		results = append(results, discordMemberToolResult(member))
	}
	return mustJSON(map[string]any{"matches": results}), nil
}

func discordMemberToolResult(member *discordgo.Member) map[string]any {
	if member == nil || member.User == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":              member.User.ID,
		"username":        member.User.Username,
		"global_name":     member.User.GlobalName,
		"server_nickname": member.Nick,
		"display_name":    discordMemberDisplayName(member),
		"is_bot":          member.User.Bot,
	}
}

func discordMemberDisplayName(member *discordgo.Member) string {
	if member == nil || member.User == nil {
		return ""
	}
	if member.Nick != "" {
		return member.Nick
	}
	if member.User.GlobalName != "" {
		return member.User.GlobalName
	}
	return member.User.Username
}

func normalizeDiscordUserID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "<@")
	value = strings.TrimPrefix(value, "!")
	value = strings.TrimSuffix(value, ">")
	if len(value) < 15 || len(value) > 22 {
		return ""
	}
	if _, err := strconv.ParseUint(value, 10, 64); err != nil {
		return ""
	}
	return value
}

func loadGarminSkill(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "metrolist", "support":
		data, err := garminSkillFiles.ReadFile("garmin_skills/" + name + ".md")
		if err != nil {
			return "", fmt.Errorf("loading skill: %w", err)
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("unknown skill %q; available skills: metrolist, support", name)
	}
}

func formatGarminAIUsage(result *garminAIResult) string {
	var prefix strings.Builder
	if len(result.Skills) > 0 {
		fmt.Fprintf(&prefix, "-# used %d skills\n", len(result.Skills))
	}
	if result.ToolCalls > 0 {
		fmt.Fprintf(&prefix, "-# used %d tools\n", result.ToolCalls)
	}
	if result.MemoryUpdated {
		prefix.WriteString("-# memory updated\n")
	}
	return prefix.String()
}

func normalizeGarminAIAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	answer = strings.NewReplacer(" — ", ", ", "—", ",", " – ", " - ", "–", "-").Replace(answer)
	lines := strings.Split(answer, "\n")
	for len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "-# ") {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func truncateGarminAIToolResult(output string) string {
	if len(output) <= garminAIToolResultSize {
		return output
	}
	return output[:garminAIToolResultSize] + `\n{"truncated":true}`
}

func toolError(err error) string {
	return mustJSON(map[string]string{"error": err.Error()})
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `{"error":"failed to encode result"}`
	}
	return string(data)
}

var garminAITools = []cmd.GarminAITool{
	garminTool("get_metrolist_status", "Get live Metrolist repository status, latest release, and recent commits. Use for current project status, activity, versions, and releases.", `{"type":"object","properties":{},"additionalProperties":false}`),
	garminTool("search_metrolist_issues", "Search current and past issues in the official Metrolist GitHub repository.", `{"type":"object","properties":{"query":{"type":"string","description":"Short GitHub issue search terms, optionally including is:open or is:closed"}},"required":["query"],"additionalProperties":false}`),
	garminTool("get_github_user", "Get a public GitHub profile by exact GitHub username. Do not use it to guess which Discord member owns an account.", `{"type":"object","properties":{"username":{"type":"string"}},"required":["username"],"additionalProperties":false}`),
	garminTool("list_notes", "List every saved Metrobot note name. Use before get_note when the relevant note name is unknown.", `{"type":"object","properties":{},"additionalProperties":false}`),
	garminTool("get_note", "Read a saved Metrobot note by exact name.", `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
	garminTool("get_discord_member", "Get exact username, global name, server nickname, and display name for a Discord member ID or mention.", `{"type":"object","properties":{"user_id":{"type":"string"}},"required":["user_id"],"additionalProperties":false}`),
	garminTool("search_discord_members", "Search server members by the beginning of a username or nickname. Results may be ambiguous, so do not claim a match when several are returned.", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
	garminTool("load_skill", "Load focused reference instructions. Available skills: metrolist for project facts and official resources, support for troubleshooting.", `{"type":"object","properties":{"name":{"type":"string","enum":["metrolist","support"]}},"required":["name"],"additionalProperties":false}`),
	garminTool("remember", "Append durable Markdown memory. Only works when the current user is a bot admin and explicitly asked to save durable, non-sensitive information.", `{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}`),
}

func garminTool(name, description, schema string) cmd.GarminAITool {
	return cmd.GarminAITool{
		Type: "function",
		Function: cmd.GarminAIFunctionDefinition{
			Name:        name,
			Description: description,
			Parameters:  json.RawMessage(schema),
		},
	}
}
