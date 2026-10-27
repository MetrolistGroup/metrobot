package discord

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/internal/decancer"
	"github.com/MetrolistGroup/metrobot/util"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

var chatModPattern = regexp.MustCompile(`(?i)^!(ban|dban|tban|sban|kick|mute|timeout|warn)\s*(.*)$`)

const (
	discordCoolPeopleRoleID = "1530657656526737548"
	discordModeratorRoleID  = "1495442011749220563"
	blockedGuildTagID       = "1469982893056200819"
	donorRoleID             = "1528525769570127872"
	donorWelcomeChannelID   = "1528531338196418683"
	donorWelcomeNote        = "testertour"
)

var garminDirectSlurPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bn[^a-z0-9]*[i1!][^a-z0-9]*[gq][^a-z0-9]*[gq][^a-z0-9]*(?:[e3][^a-z0-9]*r|[a4@])s?\b`),
	regexp.MustCompile(`\bf[^a-z0-9]*[a4@][^a-z0-9]*g(?:[^a-z0-9]*g)?(?:[^a-z0-9]*[o0][^a-z0-9]*t)?s?\b`),
	regexp.MustCompile(`\b(?:k[i1!]ke|ch[i1!]nk|tr[a4@]nn(?:y|ie)|r[e3]t[a4@]rd(?:ed)?)s?\b`),
}

var newMemberDehoistDelays = []time.Duration{
	2 * time.Second,
	10 * time.Second,
	30 * time.Second,
	2 * time.Minute,
}

func (b *Bot) onInteractionCreate(s *bot.Client, i *events.ApplicationCommandInteractionCreate) {
	if snowflakeString(i.GuildID()) != b.Config.DiscordGuildID {
		return
	}
	if i.Member() == nil {
		return
	}
	if i.Data.Type() == discord.ApplicationCommandTypeMessage {
		switch i.Data.CommandName() {
		case "Clip":
			b.handleQuoteInteraction(s, i)
		case "OCR Translate":
			b.handleOCRTranslate(s, i)
		}
		return
	}
	if i.Data.Type() != discord.ApplicationCommandTypeSlash {
		return
	}
	data := i.SlashCommandInteractionData()
	callerID := i.User().ID.String()
	opts := data.Options

	b.Logger.Info("slash command",
		zap.String("command", data.CommandName()),
		zap.String("user", callerID),
	)

	stay := getOptBool(opts, "stay") && b.DB.IsAdmin("discord", callerID, b.Config)

	switch data.CommandName() {
	case "help":
		b.handleHelp(s, i)
	case "gsm":
		b.handleGSMArena(s, i, opts)
	case "nanoreview":
		b.handleHardware(s, i, "nano", opts)
	case "technicalcity":
		b.handleHardware(s, i, "tc", opts)
	case "gsmcompare":
		b.handleCompare(s, i, "gsm", opts)
	case "nanoreviewcompare":
		b.handleCompare(s, i, "nano", opts)
	case "technicalcitycompare":
		b.handleCompare(s, i, "tc", opts)
	case "ctx-reset":
		b.handleGarminContextResetInteraction(s, i, callerID)
	case "quote", "Clip":
		b.handleQuoteInteraction(s, i)
	case "nuke":
		b.handleNukeInteraction(s, i)
	case "notes":
		b.handleNotes(s, i)
	case "note":
		b.handleNote(s, i, opts, callerID, stay)
	case "addnote":
		b.handleAddNote(s, i, opts, callerID)
	case "editnote":
		b.handleEditNote(s, i, opts, callerID)
	case "renamenote":
		b.handleRenameNote(s, i, opts, callerID)
	case "delnote":
		b.handleDelNote(s, i, opts, callerID)
	case "version":
		b.handleVersion(s, i, opts, stay)
	case "latest":
		b.handleLatest(s, i, stay)
	case "actions":
		b.handleActions(s, i, stay)
	case "ban":
		b.handleBan(s, i, opts, callerID)
	case "dban":
		b.handleDBan(s, i, opts, callerID)
	case "tban":
		b.handleTBan(s, i, opts, callerID)
	case "sban":
		b.handleSBan(s, i, opts, callerID)
	case "kick":
		b.handleKick(s, i, opts, callerID)
	case "mute", "timeout":
		b.handleMute(s, i, opts, callerID)
	case "warn":
		b.handleWarn(s, i, opts, callerID)
	case "warnings":
		b.handleWarnings(s, i, opts)
	case "unwarn":
		b.handleUnwarn(s, i, opts, callerID)
	case "dehoist":
		b.handleDehoist(s, i, opts, callerID)
	case "approvenick":
		b.handleApproveNick(s, i, opts, callerID)
	case "bulkrole":
		b.handleBulkRole(s, i, opts, callerID)
	case "addadmin":
		b.handleAddAdmin(s, i, opts, callerID)
	case "removeadmin":
		b.handleRemoveAdmin(s, i, opts, callerID)
	case "memory":
		b.handleGarminMemory(s, i, data, callerID)
	case "ping":
		b.handlePing(s, i)
	case "purge":
		b.handlePurge(s, i, opts, callerID)
	case "scanreactions":
		b.handleScanReactions(s, i, callerID)
	case "refreshstarboard":
		b.handleRefreshStarboard(s, i, callerID)
	}
}

func (b *Bot) onMessageCreate(s *bot.Client, m *discord.Message) {
	if m.Author.ID == 0 || m.Author.Bot || m.WebhookID != nil || snowflakeString(m.GuildID) != b.Config.DiscordGuildID {
		return
	}
	if b.repostCleanYouTubeLinks(s, m) {
		return
	}

	content := strings.TrimSpace(m.Content)
	if isNukeTrigger(content) {
		b.cancelGarminAIAmbient(m)
		go b.handleNukeReply(s, m)
		return
	}
	if isQuoteTrigger(content) {
		go b.handleQuoteReply(s, m)
		return
	}
	if strings.EqualFold(content, "ok garmin ctx-reset") || strings.EqualFold(content, "France Modération ctx-reset") {
		b.handleGarminContextResetMessage(s, m)
		return
	}
	if strings.EqualFold(content, "ok garmin kill") || strings.EqualFold(content, "France Modération guillotine") {
		b.handleGarminKill(s, m)
		return
	}
	if prompt, triggered := extractGarminPrompt(s, content); triggered {
		b.cancelGarminAIAmbient(m)
		if b.autoWarnGarminAbuse(s, m, prompt) {
			return
		}
		go b.handleGarminAI(s, m, b.garminAITriggeredConversation(m, prompt))
		return
	}
	if m.ChannelID.String() != garminAppSupportID {
		if messages, continuation := b.garminAIContinuation(m, content); continuation {
			b.cancelGarminAIAmbient(m)
			if b.autoWarnGarminAbuse(s, m, content) {
				return
			}
			go b.handleGarminAI(s, m, messages)
			return
		}
	}

	// Check for "Ok Garmin" trigger (case insensitive, comma optional)
	content = b.garminProcessor.ProcessTrigger(content)

	if noteName := extractNoteName(content); noteName != "" {
		note, err := b.Notes.GetNoteDocument(noteName)
		if err != nil {
			b.Logger.Debug("note not found", zap.String("note", noteName), zap.Error(err))
			return
		}
		b.sendNoteReply(s, m.ChannelID.String(), m.ID.String(), noteName, note.Render())
		return
	}
	matches := chatModPattern.FindStringSubmatch(content)
	if matches == nil {
		if m.ChannelID.String() == garminAppSupportID {
			messages := b.garminAITriggeredConversation(m, content)
			go b.handleGarminAutomaticAppSupport(s, m, messages)
			return
		}
		if garminAIAmbientTargetsOtherUser(s, m) || b.stopGarminAIAmbient(m, content) || !garminAIAmbientEnabled(s, m.ChannelID.String()) {
			return
		}
		if messages, active := b.garminAIAmbientContinuation(m, content); active {
			ambientToken := b.tryBeginGarminAIAmbient(m)
			if ambientToken == 0 {
				return
			}
			if b.autoWarnGarminAbuse(s, m, content) {
				b.endGarminAIAmbient(m, ambientToken)
				return
			}
			go func() {
				defer b.endGarminAIAmbient(m, ambientToken)
				b.handleGarminAIWithMode(s, m, messages, true, ambientToken)
			}()
		}
		return
	}

	action := strings.ToLower(matches[1])
	args := strings.TrimSpace(matches[2])
	callerID := m.Author.ID.String()
	member := m.Member
	if member == nil {
		member = garminCurrentGuildMember(s, snowflakeString(m.GuildID), callerID)
	}

	if !b.canUseModerationAction(member, callerID, action) {
		sendReply(s, m.ChannelID.String(), m.ID.String(), "You don't have permission to use that moderation command.", false, b.Logger)
		return
	}

	if args == "" {
		usageMap := map[string]string{
			"ban":     "ban - usage: ban [user] [reason]",
			"dban":    "dban - usage: dban [user] [reason]",
			"tban":    "tban - usage: tban [user] [duration] [reason]",
			"sban":    "sban - usage: sban [user] [reason]",
			"kick":    "kick - usage: kick [user] [reason]",
			"mute":    "mute - usage: mute [user] [duration] [reason]",
			"timeout": "timeout - usage: timeout [user] [duration] [reason]",
			"warn":    "warn - usage: warn [user] [reason]",
		}
		sendReply(s, m.ChannelID.String(), m.ID.String(), usageMap[action], false, b.Logger)
		return
	}

	var targetID string
	var commandArgs string

	// Check if this is a reply
	if m.ReferencedMessage != nil {
		// Use the replied-to user as target
		targetID = m.ReferencedMessage.Author.ID.String()
		// Full args string is the reason
		commandArgs = args
	} else {
		// Normal mode: first arg is target, rest is reason
		parts := strings.Fields(args)
		if len(parts) == 0 {
			sendReply(s, m.ChannelID.String(), m.ID.String(), "Please specify a target user or reply to a message.", false, b.Logger)
			return
		}
		targetID = extractUserID(parts[0])
		if len(parts) > 1 {
			commandArgs = strings.Join(parts[1:], " ")
		}
	}

	if targetID == "" {
		sendReply(s, m.ChannelID.String(), m.ID.String(), "Could not resolve target user.", false, b.Logger)
		return
	}

	if b.DB.IsAdmin("discord", targetID, b.Config) {
		sendReply(s, m.ChannelID.String(), m.ID.String(), "I will not take action against an admin.", false, b.Logger)
		return
	}

	// Execute the command directly
	b.executePrefixCommand(s, m.ChannelID.String(), m.Author.ID.String(), action, commandArgs, targetID)
}

func (b *Bot) handleGarminKill(s *bot.Client, m *discord.Message) {
	if m.ReferencedMessage == nil || m.ReferencedMessage.Author.ID == 0 {
		return
	}
	var timeout time.Duration
	var timeoutReason string
	if b.garminKillStaff(s, m) {
		timeout = 30 * time.Second
		timeoutReason = "30-second timeout"
	} else if m.Member != nil && slices.Contains(m.Member.RoleIDs, snowflakeID(discordCoolPeopleRoleID)) {
		timeout = 5 * time.Second
		timeoutReason = "5-second timeout"
	} else {
		return
	}
	targetID := m.ReferencedMessage.Author.ID.String()
	until := time.Now().Add(timeout)
	if err := updateTimeout(s, value(m.GuildID), snowflakeID(targetID), &until); err != nil {
		b.Logger.Error("Garmin kill timeout failed", zap.String("target", targetID), zap.Error(err))
		return
	}
	if err := s.Rest.DeleteMessage(m.ChannelID, m.ReferencedMessage.ID); err != nil {
		b.Logger.Error("Garmin kill message deletion failed", zap.String("message", m.ReferencedMessage.ID.String()), zap.Error(err))
		return
	}
	if b.DB != nil {
		_ = b.DB.LogModAction("discord", m.Author.ID.String(), targetID, "kill", timeoutReason)
	}
	sendReply(s, m.ChannelID.String(), m.ID.String(), "Target eliminated.", false, b.Logger)
}

func (b *Bot) garminKillStaff(s *bot.Client, m *discord.Message) bool {
	if b.canUseModerationAction(m.Member, m.Author.ID.String(), "kill") {
		return true
	}
	member := garminCurrentGuildMember(s, snowflakeString(m.GuildID), m.Author.ID.String())
	channel, err := getDiscordChannel(s, m.ChannelID.String())
	return member != nil && err == nil && s.Caches.MemberPermissionsInChannel(channel, *member).Has(discord.PermissionModerateMembers)
}

func (b *Bot) canUseModerationAction(member *discord.Member, callerID, action string) bool {
	if b.DB != nil && b.Config != nil && b.DB.IsAdmin("discord", callerID, b.Config) {
		return true
	}
	if member == nil || !slices.Contains(member.RoleIDs, snowflakeID(discordModeratorRoleID)) {
		return false
	}
	switch action {
	case "kill", "sban", "kick", "mute", "timeout":
		return true
	default:
		return false
	}
}

func (b *Bot) handleGarminContextResetMessage(s *bot.Client, m *discord.Message) {
	if b.DB == nil || !b.DB.IsAdmin("discord", m.Author.ID.String(), b.Config) {
		return
	}
	if err := b.resetGarminContext(m.ChannelID.String(), m.ID.String()); err != nil {
		b.Logger.Error("Garmin context reset failed", zap.String("channel", m.ChannelID.String()), zap.Error(err))
		sendReply(s, m.ChannelID.String(), m.ID.String(), "Couldn't reset Garmin context.", false, b.Logger)
		return
	}
	sendReply(s, m.ChannelID.String(), m.ID.String(), "Garmin context reset for this channel.", false, b.Logger)
}

func (b *Bot) handleGarminContextResetInteraction(s *bot.Client, i *events.ApplicationCommandInteractionCreate, callerID string) {
	if b.DB == nil || !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can reset Garmin context.")
		return
	}
	if err := b.resetGarminContext(i.Channel().ID().String(), i.ID().String()); err != nil {
		b.Logger.Error("Garmin context reset failed", zap.String("channel", i.Channel().ID().String()), zap.Error(err))
		respondEphemeral(s, i, "Couldn't reset Garmin context.")
		return
	}
	respondEphemeral(s, i, "Garmin context reset for this channel.")
}

func (b *Bot) autoWarnGarminAbuse(s *bot.Client, m *discord.Message, prompt string) bool {
	if !garminDirectSlur(prompt) || b.DB.IsAdmin("discord", m.Author.ID.String(), b.Config) {
		return false
	}
	reason := "Directed a prohibited slur at Metrobot"
	response, extras, _, err := b.Warn.Warn(b.newBanner(), "system", m.Author.ID.String(), reason, b.Config)
	if err != nil {
		b.Logger.Error("automatic Metrobot abuse warning failed",
			zap.String("user", m.Author.ID.String()), zap.String("message", m.ID.String()), zap.Error(err))
		return false
	}
	b.Logger.Info("automatic Metrobot abuse warning issued",
		zap.String("user", m.Author.ID.String()), zap.String("message", m.ID.String()))
	_, _ = s.Rest.CreateMessage(m.ChannelID, *noPingMessage(response))
	for _, extra := range extras {
		_, _ = s.Rest.CreateMessage(m.ChannelID, discord.MessageCreate{
			Content:         suppressDiscordEmbeds(extra),
			Flags:           discord.MessageFlagSuppressEmbeds,
			AllowedMentions: &discord.AllowedMentions{},
		})
	}
	return true
}

func garminDirectSlur(prompt string) bool {
	normalized := strings.ToLower(decancer.Cure(strings.TrimSpace(prompt)))
	if normalized == "" || containsAnyGarminPhrase(normalized,
		"what does", "what is", "what's", "define ", "meaning of", "why is", "is the word",
		"called me", "called him", "called her", "called them", "someone said", "they said",
		"he said", "she said", "quote", "quoted", "can you say", "n-word", "n word", "f-word",
		"f word", "a slur", "the slur") {
		return false
	}
	for _, pattern := range garminDirectSlurPatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}

// --- Slash command handlers ---

const helpComponentPrefix = "help:page:"

var helpPages = []struct {
	title string
	body  string
}{
	{"Notes", "• `/notes` - List all available notes\n• `/note [name]` - Show a specific note\n• `/note [name] [short_desc] [content]` - Add a note (admin only)\n• `/addnote [name] [short_desc] [content]` - Add a new note (admin only)\n• `/editnote [name] [short_desc] [content]` - Edit a note (admin only)\n• `/renamenote [name] [new_name]` - Rename a note (admin only)\n• `/delnote [name]` - Delete a note (admin only)\n• All saved notes are available in #app-support"},
	{"Bot info", "• `/gsm [search]` - Look up phone specifications on GSMArena\n• `/nanoreview [search]` - Look up devices on NanoReview\n• `/technicalcity [search]` - Look up CPUs and GPUs on Technical City\n• `/gsmcompare`, `/nanoreviewcompare`, `/technicalcitycompare` - Compare two devices on one source\n• `/quote` - Turn the previous message into a quote image\n• `/nuke [user]` or reply `garmin nuke them` - Send a cartoon nuke GIF\n• Message Apps > OCR Translate - Read image text and translate it into English\n• `/version [version]` - Show release info\n• `/latest` - Show the latest release\n• `/actions` - Show GitHub Actions build status\n• `/ping` - Check latency to various services"},
	{"Moderation", "• `/ban [user] [reason]` - Permanently ban a user\n• `/dban [user] [reason]` - Ban and delete messages\n• `/tban [user] [duration] [reason]` - Temporarily ban a user\n• `/sban [user] [reason]` - Softban a user (admin/moderator)\n• `/kick [user] [reason]` - Kick a user (admin/moderator)\n• `/timeout [user] [duration] [reason]` - Timeout a user (admin/moderator)\n• `/mute [user] [duration] [reason]` - Timeout alias (admin/moderator)\n• `/warn [user] [reason]` - Warn a user\n• `/warnings [user]` - Show warnings for a user\n• `/unwarn [user] [id]` - Remove a warning from a user\n• `/dehoist [user] [dry]` - Dehoist a user, or omit user to rerun the server\n• `/approvenick [user]` - Allow a user's next nickname change without dehoisting\n• `/bulkrole [role] [users]` - Add a lower role to multiple users (Manage Roles required)\n• `/purge [count]` - Delete recent messages\n• `/scanreactions` - Scan recent messages for prohibited reactions"},
	{"Admin and triggers", "**Admin management (permaadmin only)**\n• `/addadmin [user]` - Add a bot admin\n• `/removeadmin [user]` - Remove a bot admin\n\n**Metrobot AI (admin only)**\n• `/memory view` - View shared AI memory (admin only)\n• `/memory append|replace|clear` - Update shared AI memory (Nyx and Lamp only)\n• `/ctx-reset`, `ok garmin ctx-reset` or `France Modération ctx-reset` - Forget earlier AI context in this channel\n\n**Prefix commands**\nModeration actions can also use `!action [user] [args]`, such as `!ban @user spam`.\n\n**Note triggers**\nType `.notename` to display a note, such as `.help` or `.rules`."},
}

func helpComponents(page int) ([]discord.LayoutComponent, bool) {
	if page < 0 || page >= len(helpPages) {
		return nil, false
	}
	previous, next := page-1, page+1
	if previous < 0 {
		previous = 0
	}
	if next >= len(helpPages) {
		next = len(helpPages) - 1
	}
	return []discord.LayoutComponent{discord.ContainerComponent{Components: []discord.ContainerSubComponent{
		discord.TextDisplayComponent{Content: "# Metrobot help\n## " + helpPages[page].title + "\n" + helpPages[page].body},
		discord.SeparatorComponent{},
		discord.TextDisplayComponent{Content: fmt.Sprintf("-# Page %d of %d", page+1, len(helpPages))},
		discord.ActionRowComponent{Components: []discord.InteractiveComponent{
			discord.ButtonComponent{Label: "Previous", Style: discord.ButtonStyleSecondary, CustomID: helpComponentPrefix + strconv.Itoa(previous), Disabled: page == 0},
			discord.ButtonComponent{Label: "Next", Style: discord.ButtonStyleSecondary, CustomID: helpComponentPrefix + strconv.Itoa(next), Disabled: page == len(helpPages)-1},
		}},
	}}}, true
}

func (b *Bot) handleHelp(s *bot.Client, i *events.ApplicationCommandInteractionCreate) {
	components, _ := helpComponents(0)
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Components:      components,
			Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
			AllowedMentions: &discord.AllowedMentions{},
		},
	}); err != nil {
		b.Logger.Error("failed to show help", zap.Error(err))
	}
}

func (b *Bot) handleHelpComponent(s *bot.Client, i *events.ComponentInteractionCreate) bool {
	customID := i.Data.CustomID()
	if !strings.HasPrefix(customID, helpComponentPrefix) {
		return false
	}
	page, err := strconv.Atoi(strings.TrimPrefix(customID, helpComponentPrefix))
	components, ok := helpComponents(page)
	if err != nil || !ok {
		respondEphemeral(s, i, "That help page is unavailable.")
		return true
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeUpdateMessage,
		Data: discord.MessageCreate{
			Components:      components,
			Flags:           discord.MessageFlagIsComponentsV2,
			AllowedMentions: &discord.AllowedMentions{},
		},
	}); err != nil {
		b.Logger.Error("failed to change help page", zap.Error(err))
	}
	return true
}

func (b *Bot) handleNotes(s *bot.Client, i *events.ApplicationCommandInteractionCreate) {
	text, err := b.Notes.ListNotes()
	if err != nil {
		b.Logger.Error("notes error", zap.Error(err))
		respondEphemeral(s, i, "Error listing notes.")
		return
	}
	respondEphemeral(s, i, text)
}

func (b *Bot) handleNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string, stay bool) {
	name := opts["name"].String()
	shortDesc, hasShortDesc := opts["short_desc"]
	content, hasContent := opts["content"]
	if hasShortDesc || hasContent {
		if !hasShortDesc || !hasContent {
			respondEphemeral(s, i, "Both short_desc and content are required when adding a note.")
			return
		}
		if !b.DB.IsAdmin("discord", callerID, b.Config) {
			respondEphemeral(s, i, "Only admins can add notes.")
			return
		}
		if err := b.Notes.AddNote(name, shortDesc.String(), content.String()); err != nil {
			respondPublic(s, i, fmt.Sprintf("Error adding note: %s", err))
			return
		}
		respondPublic(s, i, fmt.Sprintf("Note `%s` added.", strings.ToLower(name)))
		return
	}

	note, err := b.Notes.GetNoteDocument(name)
	if err != nil {
		b.Logger.Error("note error", zap.Error(err))
		respondEphemeral(s, i, "Error fetching note.")
		return
	}

	b.respondNote(s, i, name, note.Render(), !stay)
}

func (b *Bot) handleAddNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can add notes.")
		return
	}
	name := opts["name"].String()
	shortDesc := opts["short_desc"].String()
	content := opts["content"].String()
	if err := b.Notes.AddNote(name, shortDesc, content); err != nil {
		respondPublic(s, i, fmt.Sprintf("Error adding note: %s", err))
		return
	}
	respondPublic(s, i, fmt.Sprintf("Note `%s` added.", strings.ToLower(name)))
}

func (b *Bot) handleEditNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can edit notes.")
		return
	}
	name := opts["name"].String()
	shortDesc := opts["short_desc"].String()
	content := opts["content"].String()
	if err := b.Notes.EditNote(name, shortDesc, content); err != nil {
		respondPublic(s, i, fmt.Sprintf("Error editing note: %s", err))
		return
	}
	respondPublic(s, i, fmt.Sprintf("Note `%s` updated.", strings.ToLower(name)))
}

func (b *Bot) handleRenameNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can rename notes.")
		return
	}
	name := opts["name"].String()
	newName := opts["new_name"].String()
	if err := b.Notes.RenameNote(name, newName); err != nil {
		respondPublic(s, i, fmt.Sprintf("Error renaming note: %s", err))
		return
	}
	respondPublic(s, i, fmt.Sprintf("Note `%s` renamed to `%s`.", strings.ToLower(name), strings.ToLower(newName)))
}

func (b *Bot) handleDelNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can delete notes.")
		return
	}
	name := opts["name"].String()
	if err := b.Notes.DeleteNote(name); err != nil {
		respondPublic(s, i, fmt.Sprintf("Error deleting note: %s", err))
		return
	}
	respondPublic(s, i, fmt.Sprintf("Note `%s` deleted.", strings.ToLower(name)))
}

func (b *Bot) handleVersion(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, stay bool) {
	tag := "latest"
	if opt, ok := opts["version"]; ok {
		tag = opt.String()
	}

	text, err := b.Version.GetVersion(context.Background(), tag, false)
	if err != nil {
		b.Logger.Error("version error", zap.Error(err))
		respondEphemeral(s, i, "Error fetching version info.")
		return
	}

	if stay {
		respondPublic(s, i, text)
	} else {
		respondEphemeral(s, i, text)
	}
}

func (b *Bot) handleLatest(s *bot.Client, i *events.ApplicationCommandInteractionCreate, stay bool) {
	text, err := b.Version.GetVersion(context.Background(), "latest", false)
	if err != nil {
		b.Logger.Error("latest error", zap.Error(err))
		respondEphemeral(s, i, "Error fetching latest version.")
		return
	}

	if stay {
		respondPublic(s, i, text)
	} else {
		respondEphemeral(s, i, text)
	}
}

func (b *Bot) handleActions(s *bot.Client, i *events.ApplicationCommandInteractionCreate, stay bool) {
	text, err := b.Actions.GetActions(context.Background(), false)
	if err != nil {
		b.Logger.Error("actions error", zap.Error(err))
		respondEphemeral(s, i, "Error fetching actions status.")
		return
	}

	if stay {
		respondPublic(s, i, text)
	} else {
		respondEphemeral(s, i, text)
	}
}

func (b *Bot) handleBan(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have ban permissions.")
		return
	}
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")
	banner := b.newBanner()

	resp, _, err := b.Moderation.Ban(banner, callerID, targetUser.String(), reason, b.Config)
	if err != nil {
		b.Logger.Error("ban failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing ban.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleDBan(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have ban permissions.")
		return
	}
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")
	banner := b.newBanner()

	resp, _, err := b.Moderation.DBan(banner, callerID, targetUser.String(), reason, b.Config)
	if err != nil {
		b.Logger.Error("dban failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing dban.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleTBan(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have ban permissions.")
		return
	}
	targetUser := opts["user"].Snowflake()
	durationStr := opts["duration"].String()
	reason := getOptString(opts, "reason")

	dur, err := util.ParseDuration(durationStr)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("Invalid duration: %s", err))
		return
	}

	banner := b.newBanner()
	resp, _, err := b.Moderation.TBan(banner, callerID, targetUser.String(), dur, reason, b.Config)
	if err != nil {
		b.Logger.Error("tban failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing tban.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleSBan(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.canUseModerationAction(interactionMember(i), callerID, "sban") {
		respondEphemeral(s, i, "You don't have softban permissions.")
		return
	}
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")
	banner := b.newBanner()

	resp, _, err := b.Moderation.SBan(banner, callerID, targetUser.String(), reason, b.Config)
	if err != nil {
		b.Logger.Error("sban failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing sban.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleKick(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.canUseModerationAction(interactionMember(i), callerID, "kick") {
		respondEphemeral(s, i, "You don't have kick permissions.")
		return
	}
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")

	resp, _, err := b.Moderation.Kick(b.newBanner(), callerID, targetUser.String(), reason, b.Config)
	if err != nil {
		b.Logger.Error("kick failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing kick.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleMute(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.canUseModerationAction(interactionMember(i), callerID, "timeout") {
		respondEphemeral(s, i, "You don't have timeout permissions.")
		return
	}
	targetUser := opts["user"].Snowflake()
	durationStr := opts["duration"].String()
	reason := getOptString(opts, "reason")

	dur, err := util.ParseDuration(durationStr)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("Invalid duration: %s", err))
		return
	}

	banner := b.newBanner()
	resp, _, err := b.Moderation.Mute(banner, callerID, targetUser.String(), dur, reason, b.Config)
	if err != nil {
		b.Logger.Error("mute failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing mute.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleWarn(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have warn permissions.")
		return
	}
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")
	banner := b.newBanner()

	resp, extras, _, err := b.Warn.Warn(banner, callerID, targetUser.String(), reason, b.Config)
	if err != nil {
		b.Logger.Error("warn failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing warn.")
		return
	}
	respondPublic(s, i, resp)
	for _, extra := range extras {
		s.Rest.CreateMessage(i.Channel().ID(), discord.MessageCreate{Content: suppressDiscordEmbeds(extra), Flags: discord.MessageFlagSuppressEmbeds})
	}
}

func (b *Bot) handleWarnings(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption) {
	targetUser := opts["user"].Snowflake()
	banner := b.newBanner()
	resp, err := b.Warn.Warnings(banner, targetUser.String())
	if err != nil {
		b.Logger.Error("warnings error", zap.Error(err))
		respondEphemeral(s, i, "Error fetching warnings.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleUnwarn(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have unwarn permissions.")
		return
	}
	targetUser := opts["user"].Snowflake()
	index := int(opts["id"].Int())
	banner := b.newBanner()

	resp, _, err := b.Warn.Unwarn("discord", callerID, targetUser.String(), index, banner)
	if err != nil {
		b.Logger.Error("unwarn error", zap.Error(err))
		respondEphemeral(s, i, fmt.Sprintf("Error: %s", err))
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleDehoist(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have dehoist permissions.")
		return
	}

	dry := getOptBool(opts, "dry")
	var targetID string
	if opt, ok := opts["user"]; ok {
		targetID = opt.Snowflake().String()
	}

	if err := deferResponse(s, i, dry); err != nil {
		b.Logger.Error("failed to defer dehoist interaction", zap.Error(err))
		return
	}

	banner := b.newBanner()
	resp, err := b.Moderation.Dehoist(banner, targetID, dry, b.Config)
	if err != nil {
		b.Logger.Error("dehoist error", zap.Error(err))
		if editErr := editDeferredResponse(s, i, "Error executing dehoist."); editErr != nil {
			b.Logger.Error("failed to edit deferred dehoist response", zap.Error(editErr))
		}
		return
	}

	if dry && len(resp) > 2000 {
		chunks := chunkString(resp, 2000)
		for _, chunk := range chunks {
			dmUser(s, callerID, chunk)
		}
		if err := editDeferredResponse(s, i, "The output is too long, so I sent it to your DMs."); err != nil {
			b.Logger.Error("failed to edit deferred dehoist response", zap.Error(err))
		}
		return
	}

	if err := editDeferredResponse(s, i, resp); err != nil {
		b.Logger.Error("failed to edit deferred dehoist response", zap.Error(err))
	}
}

func (b *Bot) handleApproveNick(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have nickname approval permissions.")
		return
	}

	targetID := opts["user"].Snowflake().String()
	if err := b.DB.ApproveNicknameChange(snowflakeString(i.GuildID()), targetID); err != nil {
		b.Logger.Error("failed to approve nickname change", zap.String("userID", targetID), zap.Error(err))
		respondEphemeral(s, i, "Could not approve the nickname change.")
		return
	}
	respondEphemeral(s, i, fmt.Sprintf("Approved <@%s>'s next nickname change.", targetID))
}

func (b *Bot) handleBulkRole(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if i.Member() == nil || i.Member().Permissions&(discord.PermissionAdministrator|discord.PermissionManageRoles) == 0 {
		respondEphemeral(s, i, "You need Manage Roles to use this command.")
		return
	}

	users := parseBulkRoleUsers(opts["users"].String())
	if len(users) == 0 {
		respondEphemeral(s, i, "Provide at least one username, mention, or ID.")
		return
	}
	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Error("failed to defer bulkrole interaction", zap.Error(err))
		return
	}

	roles, err := s.Rest.GetRoles(*i.GuildID())
	if err != nil {
		b.Logger.Error("failed to get roles for bulkrole", zap.Error(err))
		_ = editDeferredResponse(s, i, "Couldn't load server roles.")
		return
	}

	roleID := opts["role"].Snowflake()
	var role *discord.Role
	for _, candidate := range roles {
		if candidate.ID == roleID {
			role = &candidate
			break
		}
	}
	if !canAssignRole(i.Member(), role, roles, snowflakeString(i.GuildID())) {
		_ = editDeferredResponse(s, i, "That role must be editable and below your highest role.")
		return
	}

	members, err := b.newBanner().GetAllMembers()
	if err != nil {
		b.Logger.Error("failed to get members for bulkrole", zap.Error(err))
		_ = editDeferredResponse(s, i, "Couldn't load server members.")
		return
	}
	userIDs, missing := resolveBulkRoleUsers(users, members)
	if len(userIDs) == 0 {
		_ = editDeferredResponse(s, i, "Couldn't find any matching server members.")
		return
	}

	succeeded := 0
	for _, userID := range userIDs {
		// ponytail: sequential edits respect Discord rate limits; add workers only if large runs become too slow.
		if err := s.Rest.AddMemberRole(value(i.GuildID()), snowflakeID(userID), role.ID, rest.WithReason("Bulk role add by "+callerID)); err != nil {
			b.Logger.Warn("bulkrole add failed", zap.String("user", userID), zap.String("role", role.ID.String()), zap.Error(err))
			continue
		}
		succeeded++
	}

	result := fmt.Sprintf("Added role %q to %d users.", role.Name, succeeded)
	if failed := len(userIDs) - succeeded; failed > 0 {
		result += fmt.Sprintf(" %d role updates failed.", failed)
	}
	if len(missing) > 0 {
		shown := missing[:min(len(missing), 10)]
		result += fmt.Sprintf(" Couldn't find %d: @%s.", len(missing), strings.Join(shown, ", @"))
		if len(missing) > len(shown) {
			result += fmt.Sprintf(" (%d more)", len(missing)-len(shown))
		}
	}
	if err := editDeferredResponse(s, i, result); err != nil {
		b.Logger.Error("failed to edit bulkrole response", zap.Error(err))
	}
}

func (b *Bot) handleAddAdmin(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	banner := b.newBanner()
	resp, err := b.Admin.AddAdmin(banner, callerID, targetUser.String(), b.Config)
	if err != nil {
		b.Logger.Error("addadmin error", zap.Error(err))
		respondEphemeral(s, i, "Error adding admin.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleRemoveAdmin(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	banner := b.newBanner()
	resp, err := b.Admin.RemoveAdmin(banner, callerID, targetUser.String(), b.Config)
	if err != nil {
		b.Logger.Error("removeadmin error", zap.Error(err))
		respondEphemeral(s, i, "Error removing admin.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleGarminMemory(s *bot.Client, i *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData, callerID string) {
	if data.SubCommandName == nil {
		respondEphemeral(s, i, "Choose a memory action.")
		return
	}

	if b.garminMemory == nil {
		respondEphemeral(s, i, "Metrobot AI is not configured.")
		return
	}
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can manage Metrobot's memory.")
		return
	}

	subcommand := *data.SubCommandName
	if subcommand != "view" && !isGarminOwner(callerID) {
		respondEphemeral(s, i, "Only Nyx and Lamp can update shared Metrobot memory.")
		return
	}
	subopts := data.Options
	var err error
	switch subcommand {
	case "view":
		var memory string
		memory, err = b.garminMemory.Read()
		if err == nil {
			if responseErr := respondGarminMemory(s, i, memory); responseErr != nil {
				b.Logger.Error("failed to send Metrobot memory", zap.Error(responseErr))
			}
			return
		}
	case "append":
		err = b.garminMemory.Append(getOptString(subopts, "content"))
		if err == nil {
			respondEphemeral(s, i, "-# memory updated\nMetrobot memory updated.")
			return
		}
	case "replace":
		err = b.garminMemory.Replace(getOptString(subopts, "content"))
		if err == nil {
			respondEphemeral(s, i, "-# memory updated\nMetrobot memory replaced.")
			return
		}
	case "clear":
		err = b.garminMemory.Clear()
		if err == nil {
			respondEphemeral(s, i, "-# memory updated\nMetrobot memory cleared.")
			return
		}
	default:
		err = fmt.Errorf("unknown memory action %q", subcommand)
	}

	b.Logger.Error("Metrobot memory command failed", zap.String("action", subcommand), zap.Error(err))
	respondEphemeral(s, i, fmt.Sprintf("Could not manage Metrobot memory: %s", err))
}

func respondGarminMemory(s *bot.Client, i *events.ApplicationCommandInteractionCreate, memory string) error {
	if len(memory) <= 1900 {
		return s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
			Type: discord.InteractionResponseTypeCreateMessage,
			Data: discord.MessageCreate{
				Content: "```md\n" + memory + "\n```",
				Flags:   discord.MessageFlagEphemeral | discord.MessageFlagSuppressEmbeds,
			},
		})
	}
	return s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Content: "Metrobot's memory is attached.",
			Flags:   discord.MessageFlagEphemeral | discord.MessageFlagSuppressEmbeds,
			Files: []*discord.File{
				{Name: cmd.GarminMemoryFile, Reader: strings.NewReader(memory)},
			},
		},
	})
}

func (b *Bot) handlePing(s *bot.Client, i *events.ApplicationCommandInteractionCreate) {
	text, err := b.Ping.Ping()
	if err != nil {
		b.Logger.Error("ping error", zap.Error(err))
		respondEphemeral(s, i, "Error checking ping.")
		return
	}
	respondPublic(s, i, text)
}

func (b *Bot) handlePurge(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have purge permissions.")
		return
	}

	var count int = 0
	if opt, ok := opts["count"]; ok {
		count = opt.Int()
	}

	if count > 0 {
		// Delete last N messages
		if count > 100 {
			count = 100
		}

		// Defer the response
		if err := deferResponse(s, i, true); err != nil {
			b.Logger.Error("failed to defer purge response", zap.Error(err))
			return
		}

		msgs, err := s.Rest.GetMessages(i.Channel().ID(), 0, 0, 0, int(count))
		if err != nil {
			b.Logger.Error("failed to get messages for purge", zap.Error(err))
			editDeferredResponse(s, i, "Error fetching messages.")
			return
		}

		var toDelete []snowflake.ID
		for _, msg := range msgs {
			toDelete = append(toDelete, msg.ID)
		}

		if len(toDelete) > 1 {
			s.Rest.BulkDeleteMessages(i.Channel().ID(), toDelete)
		} else if len(toDelete) == 1 {
			s.Rest.DeleteMessage(i.Channel().ID(), toDelete[0])
		}

		editDeferredResponse(s, i, fmt.Sprintf("Deleted %d messages.", len(toDelete)))
	} else {
		respondEphemeral(s, i, "Please provide a count to purge.")
	}
}

func (b *Bot) handleScanReactions(s *bot.Client, i *events.ApplicationCommandInteractionCreate, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have permission to scan reactions.")
		return
	}

	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Error("failed to defer scanreactions response", zap.Error(err))
		return
	}

	result, err := b.scanProhibitedReactions(s, i.Channel().ID().String())
	if err != nil {
		b.Logger.Error("scanreactions failed", zap.Error(err))
		editDeferredResponse(s, i, fmt.Sprintf("Error scanning reactions: %s", err))
		return
	}

	resp := fmt.Sprintf(
		"Scanned %d messages. Found %d prohibited reaction(s), removed %d, and issued %d warning(s).",
		result.MessagesChecked,
		result.ReactionsFound,
		result.ReactionsRemoved,
		result.WarningsIssued,
	)
	if result.WarningsSkipped > 0 {
		resp += fmt.Sprintf(" Skipped %d admin warning(s).", result.WarningsSkipped)
	}
	if result.FetchFailures > 0 || result.RemoveFailures > 0 || result.WarnFailures > 0 {
		resp += fmt.Sprintf(" Failures: %d fetch, %d remove, %d warn.", result.FetchFailures, result.RemoveFailures, result.WarnFailures)
	}

	if err := editDeferredResponse(s, i, resp); err != nil {
		b.Logger.Error("failed to edit scanreactions response", zap.Error(err))
	}
}

func (b *Bot) handleRefreshStarboard(s *bot.Client, i *events.ApplicationCommandInteractionCreate, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have permission to refresh the starboard.")
		return
	}

	// Defer the response since this might take time
	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Error("failed to defer refreshstarboard response", zap.Error(err))
		return
	}

	if err := b.RefreshAllStarboard(s); err != nil {
		b.Logger.Error("refreshstarboard failed", zap.Error(err))
		editDeferredResponse(s, i, fmt.Sprintf("Error refreshing starboard: %s", err))
		return
	}

	editDeferredResponse(s, i, "Starboard and sobboard refreshed.")
}

// --- Helpers ---

func (b *Bot) handleAutocomplete(s *bot.Client, i *events.AutocompleteInteractionCreate) {
	if snowflakeString(i.GuildID()) != b.Config.DiscordGuildID {
		return
	}

	data := i.Data
	if data.CommandName == "gsm" || data.CommandName == "gsmcompare" {
		b.handleGSMArenaAutocomplete(s, i, data.Options)
		return
	}
	if data.CommandName == "nanoreview" || data.CommandName == "technicalcity" || data.CommandName == "nanoreviewcompare" || data.CommandName == "technicalcitycompare" {
		source := "nano"
		if data.CommandName == "technicalcity" || data.CommandName == "technicalcitycompare" {
			source = "tc"
		}
		b.handleHardwareAutocomplete(s, i, source, data.Options)
		return
	}
	if data.CommandName != "unwarn" {
		return
	}

	opts := data.Options
	userOpt, ok := opts["user"]
	if !ok {
		respondAutocomplete(s, i, nil)
		return
	}

	targetUser := userOpt.Snowflake()
	if targetUser == 0 {
		respondAutocomplete(s, i, nil)
		return
	}

	warnings, err := b.DB.GetWarnings("discord", targetUser.String())
	if err != nil {
		b.Logger.Error("autocomplete warnings error", zap.Error(err))
		respondAutocomplete(s, i, nil)
		return
	}

	choices := make([]discord.AutocompleteChoice, 0, min(len(warnings), 25))
	for idx, warning := range warnings {
		if len(choices) == 25 {
			break
		}

		reason := warning.Reason
		if reason == "" {
			reason = "no reason"
		}
		label := fmt.Sprintf("%d - %s", idx+1, truncateForChoice(reason, 90))
		choices = append(choices, discord.AutocompleteChoiceInt{
			Name:  label,
			Value: idx + 1,
		})
	}

	respondAutocomplete(s, i, choices)
}

func respondAutocomplete(s *bot.Client, i *events.AutocompleteInteractionCreate, choices []discord.AutocompleteChoice) {
	_ = i.AutocompleteResult(choices)
}

func truncateForChoice(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseBulkRoleUsers(input string) []string {
	seen := make(map[string]struct{})
	var users []string
	for _, user := range strings.FieldsFunc(input, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		user = strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(user, "<@"), ">"), "!")
		user = strings.TrimPrefix(user, "@")
		key := strings.ToLower(user)
		if user == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		users = append(users, user)
	}
	return users
}

func resolveBulkRoleUsers(users []string, members []cmd.MemberInfo) (ids, missing []string) {
	byUsername := make(map[string]string, len(members))
	for _, member := range members {
		byUsername[strings.ToLower(member.Username)] = member.UserID
	}

	seen := make(map[string]struct{})
	for _, user := range users {
		id := ""
		if len(user) >= 17 && len(user) <= 20 {
			if _, err := strconv.ParseUint(user, 10, 64); err == nil {
				id = user
			}
		}
		if id == "" {
			id = byUsername[strings.ToLower(user)]
		}
		if id == "" {
			missing = append(missing, user)
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, missing
}

func canAssignRole(member *discord.ResolvedMember, role *discord.Role, roles []discord.Role, guildID string) bool {
	if member == nil || role == nil || role.Managed || role.ID.String() == guildID || member.Permissions&(discord.PermissionAdministrator|discord.PermissionManageRoles) == 0 {
		return false
	}

	highest := -1
	for _, candidate := range roles {
		if candidate.Position > highest && slices.Contains(member.RoleIDs, candidate.ID) {
			highest = candidate.Position
		}
	}
	return role.Position < highest
}

func getOptString(opts map[string]discord.SlashCommandOption, name string) string {
	if opt, ok := opts[name]; ok {
		return opt.String()
	}
	return ""
}

func getOptBool(opts map[string]discord.SlashCommandOption, name string) bool {
	if opt, ok := opts[name]; ok {
		return opt.Bool()
	}
	return false
}

func extractUserID(mention string) string {
	mention = strings.TrimPrefix(mention, "<@")
	mention = strings.TrimPrefix(mention, "!")
	mention = strings.TrimSuffix(mention, ">")
	if _, err := strconv.ParseUint(mention, 10, 64); err == nil {
		return mention
	}
	return mention
}

func chunkString(s string, maxLen int) []string {
	var chunks []string
	for len(s) > 0 {
		if len(s) <= maxLen {
			chunks = append(chunks, s)
			break
		}
		idx := strings.LastIndex(s[:maxLen], "\n")
		if idx <= 0 {
			idx = maxLen
		}
		chunks = append(chunks, s[:idx])
		s = s[idx:]
		s = strings.TrimPrefix(s, "\n")
	}
	return chunks
}

// Discord event handlers for automatic member actions

func (b *Bot) onGuildTagEvent(s *bot.Client, member *discord.Member) {
	if member.GuildID.String() != b.Config.DiscordGuildID || member.User.ID == 0 {
		return
	}
	tag := member.User.PrimaryGuild
	if tag == nil || !value(tag.IdentityEnabled) || snowflakeString(tag.IdentityGuildID) != blockedGuildTagID {
		return
	}
	const reason = "Displayed the blocked ROCK server tag (guild 1469982893056200819)"
	if err := s.Rest.AddBan(member.GuildID, member.User.ID, 0, rest.WithReason(reason)); err != nil {
		b.Logger.Error("failed to ban member with blocked guild tag", zap.String("userID", member.User.ID.String()), zap.Error(err))
		return
	}
	b.Logger.Info("banned member with blocked guild tag", zap.String("userID", member.User.ID.String()))
}

func (b *Bot) onGuildMemberAdd(s *bot.Client, m *events.GuildMemberJoin) {
	if m.GuildID.String() != b.Config.DiscordGuildID || m.Member.User.ID == 0 {
		return
	}
	b.onGuildTagEvent(s, &m.Member)
	userID := m.Member.User.ID.String()
	go func() {
		for attempt, delay := range newMemberDehoistDelays {
			timer := time.NewTimer(delay)
			if b.lifecycle != nil {
				select {
				case <-timer.C:
				case <-b.lifecycle.Done():
					timer.Stop()
					return
				}
			} else {
				<-timer.C
			}
			if b.autoDehoistMember(s, m.GuildID.String(), userID, "new member", attempt+1) {
				return
			}
		}
	}()
}

func (b *Bot) onGuildMemberUpdate(s *bot.Client, m *events.GuildMemberUpdate) {
	if m.GuildID.String() != b.Config.DiscordGuildID || m.Member.User.ID == 0 {
		return
	}
	b.onGuildTagEvent(s, &m.Member)
	before, member := m.OldMember, m.Member
	if before.User.ID != 0 && !slices.Contains(before.RoleIDs, snowflakeID(donorRoleID)) && slices.Contains(member.RoleIDs, snowflakeID(donorRoleID)) {
		b.sendDonorWelcome(s, member.User.ID.String())
	}
	if before.User.ID != 0 && value(before.Nick) == value(member.Nick) {
		return
	}
	approved, err := b.DB.ConsumeNicknameApproval(m.GuildID.String(), member.User.ID.String())
	if err != nil {
		b.Logger.Error("failed to check nickname approval", zap.String("userID", member.User.ID.String()), zap.Error(err))
	} else if approved {
		b.Logger.Info("skipping auto-dehoist for approved nickname change", zap.String("userID", member.User.ID.String()))
		return
	}
	b.autoDehoistMember(s, m.GuildID.String(), member.User.ID.String(), "updated member", 1)
}

func (b *Bot) sendDonorWelcome(s *bot.Client, userID string) {
	note, err := b.Notes.GetNoteDocument(donorWelcomeNote)
	if err != nil {
		b.Logger.Error("failed to load donor welcome note", zap.Error(err))
		return
	}
	rendered := note.Render()
	prefix := fmt.Sprintf("<@%s>\n", userID)
	message := authoredNoteMessage(rendered, donorWelcomeNote, prefix, -1)
	message.AllowedMentions = &discord.AllowedMentions{Users: []snowflake.ID{snowflakeID(userID)}}
	_, err = s.Rest.CreateMessage(snowflakeID(donorWelcomeChannelID), message)
	if noteRequestRejected(err) && message.Flags.Has(discord.MessageFlagIsComponentsV2) {
		fallback := newDiscordNoteMessage(rendered.Text, prefix)
		fallback.AllowedMentions = message.AllowedMentions
		_, err = s.Rest.CreateMessage(snowflakeID(donorWelcomeChannelID), fallback)
	}
	if err != nil {
		b.Logger.Error("failed to send donor welcome", zap.String("userID", userID), zap.Error(err))
	}
}

func (b *Bot) autoDehoistMember(s *bot.Client, guildID, userID, source string, attempt int) bool {
	botMember, err := s.Rest.GetMember(snowflakeID(guildID), s.ID())
	if err != nil {
		b.Logger.Debug("failed to get bot member for auto-dehoist permission check",
			zap.String("userID", userID), zap.String("source", source), zap.Int("attempt", attempt), zap.Error(err))
		return false
	}

	targetMember, err := s.Rest.GetMember(snowflakeID(guildID), snowflakeID(userID))
	if err != nil {
		if restErr, ok := err.(*rest.Error); ok && restErr.Response != nil && restErr.Response.StatusCode == 404 {
			b.Logger.Debug("skipping auto-dehoist - member no longer in guild",
				zap.String("userID", userID), zap.String("source", source), zap.Int("attempt", attempt))
			return true
		}
		b.Logger.Debug("failed to get member for auto-dehoist",
			zap.String("userID", userID), zap.String("source", source), zap.Int("attempt", attempt), zap.Error(err))
		return false
	}

	if targetMember.User.ID == 0 {
		b.Logger.Debug("failed to auto-dehoist member with missing user data",
			zap.String("userID", userID), zap.String("source", source), zap.Int("attempt", attempt))
		return false
	}
	if targetMember.User.Bot || b.DB.IsAdmin("discord", userID, b.Config) {
		return true
	}

	if !canManageMember(s, guildID, botMember, targetMember) {
		b.Logger.Debug("skipping auto-dehoist - insufficient permissions",
			zap.String("userID", userID), zap.String("source", source), zap.Int("attempt", attempt), zap.String("displayName", value(targetMember.Nick)))
		return false
	}

	banner := b.newBanner()
	displayName, err := banner.GetDisplayName(userID)
	if err != nil {
		b.Logger.Error("failed to get display name for auto-dehoist",
			zap.String("userID", userID), zap.String("source", source), zap.Int("attempt", attempt), zap.Error(err))
		return false
	}

	if cmd.NeedsDehoisting(displayName) {
		b.Logger.Info("auto-dehoisting member",
			zap.String("userID", userID),
			zap.String("source", source),
			zap.Int("attempt", attempt),
			zap.String("displayName", displayName))

		_, err := b.Moderation.Dehoist(banner, userID, false, b.Config)
		if err != nil {
			b.Logger.Error("failed to auto-dehoist member",
				zap.String("userID", userID), zap.String("source", source), zap.Int("attempt", attempt), zap.Error(err))
			return false
		}
		return true
	}

	return source != "new member"
}

// canManageMember checks if the bot can manage a member's nickname
func canManageMember(s *bot.Client, guildID string, botMember, targetMember *discord.Member) bool {
	if botMember == nil || targetMember == nil || botMember.User.ID == targetMember.User.ID {
		return false
	}
	guild, err := s.Rest.GetGuild(snowflakeID(guildID), false)
	if err != nil || targetMember.User.ID == guild.OwnerID {
		return false
	}
	botHighest, targetHighest := 0, 0
	var permissions discord.Permissions
	for _, role := range guild.Roles {
		if role.ID == guild.ID || slices.Contains(botMember.RoleIDs, role.ID) {
			permissions |= role.Permissions
		}
		if slices.Contains(botMember.RoleIDs, role.ID) && role.Position > botHighest {
			botHighest = role.Position
		}
		if slices.Contains(targetMember.RoleIDs, role.ID) && role.Position > targetHighest {
			targetHighest = role.Position
		}
	}
	return (permissions.Has(discord.PermissionAdministrator) || permissions.Has(discord.PermissionManageNicknames)) && botHighest > targetHighest
}

// extractNoteName extracts note name from formats like .NOTE, . NOTE, .. NOTE, ..NOTE, etc.
func extractNoteName(content string) string {
	if !strings.HasPrefix(content, ".") {
		return ""
	}

	// Count leading '.' characters
	i := 0
	for i < len(content) && content[i] == '.' {
		i++
	}

	if i >= len(content) {
		return "" // Only dots, no note name
	}

	// Skip any whitespace after dots
	remainder := strings.TrimLeft(content[i:], " \t")
	if remainder == "" {
		return "" // No note name after dots and spaces
	}

	// Extract the first word as note name
	fields := strings.Fields(remainder)
	if len(fields) == 0 {
		return ""
	}

	return fields[0]
}
