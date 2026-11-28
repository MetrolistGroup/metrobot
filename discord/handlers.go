package discord

import (
	"context"
	"errors"
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

var chatModPattern = regexp.MustCompile(`(?i)^!(ban|dban|tban|sban|kick|mute|timeout|warn)(?:\s+(.*))?$`)

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

	stay := getOptBool(opts, "stay")

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
	if b.handleSigmaCommand(s, m, content) || b.handleCompoundSigmaCommands(s, m, content) {
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
		if len(m.Attachments) == 0 && len(m.StickerItems) == 0 {
			switch greeting := strings.ToLower(strings.Trim(prompt, " !.?")); greeting {
			case "hi", "hello", "hey":
				go b.sendGarminReplyIfVisible(s, m, greeting)
				return
			}
		}
		go b.handleGarminAI(s, m, b.garminAITriggeredConversation(m, prompt))
		return
	}
	if noteName := cmd.InlineNoteName(content); noteName != "" {
		if note, err := b.Notes.GetNoteDocument(noteName); err == nil {
			b.sendNoteReply(s, m.ChannelID.String(), m.ID.String(), noteName, note.Render())
			return
		}
	}
	// A direct reply to the bot, or any message from someone in an active support session, is explicit in app-support:
	// the chat model answers at once instead of the router.
	if b.isSupportChannel(m.ChannelID.String()) {
		botID := ""
		if s != nil && s.Caches != nil && s.ID() != 0 {
			botID = s.ID().String()
		}
		b.noteSupportHandoff(m, botID)
	}
	supportHandedOff := b.isSupportChannel(m.ChannelID.String()) && b.supportHandedOff(m)
	if b.isSupportChannel(m.ChannelID.String()) && !supportHandedOff && !strings.ContainsAny(content[:min(1, len(content))], ".!/") &&
		(garminReplyToBot(s, m) && b.garminMessageVisible(m.ChannelID.String(), m.ReferencedMessage.ID.String()) ||
			m.MessageReference == nil && m.ReferencedMessage == nil && !garminAIAmbientTargetsOtherUser(s, m) && b.continueSupportSession(m)) {
		b.cancelGarminAIAmbient(m)
		if b.autoWarnGarminAbuse(s, m, content) {
			return
		}
		go b.handleGarminSupportFollowup(s, m, b.garminAITriggeredConversation(m, content))
		return
	}
	if !b.isSupportChannel(m.ChannelID.String()) {
		if messages, continuation := b.garminAIContinuation(m, content); continuation {
			b.cancelGarminAIAmbient(m)
			if b.autoWarnGarminAbuse(s, m, content) {
				return
			}
			go b.handleGarminAI(s, m, messages)
			return
		}
		if garminReplyToBot(s, m) && b.garminMessageVisible(m.ChannelID.String(), m.ReferencedMessage.ID.String()) {
			if b.autoWarnGarminAbuse(s, m, content) {
				return
			}
			go b.handleGarminAI(s, m, []cmd.GarminAIMessage{b.garminAIUserMessage(m, content)})
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
		if b.isSupportChannel(m.ChannelID.String()) {
			if supportHandedOff {
				return
			}
			messages := b.garminAITriggeredConversation(m, content)
			go b.handleGarminAutomaticAppSupport(s, m, messages)
			return
		}
		if garminAIAmbientTargetsOtherUser(s, m) || !b.garminAIAmbientEnabled(s, m.ChannelID.String()) {
			return
		}
		if messages, active := b.garminAIAmbientContinuation(m, content); active {
			if b.stopGarminAIAmbient(m, content) {
				return
			}
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

	// Cached message members can predate a role change, so check current permissions.
	if err := b.bannerFor(s).HasPermission(callerID, action); err != nil {
		sendReply(s, m.ChannelID.String(), m.ID.String(), permissionDenial(err), false, b.Logger)
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

	// Execute the command directly
	b.executePrefixCommand(s, m.ChannelID.String(), m.Author.ID.String(), action, commandArgs, targetID)
}

func (b *Bot) handleGarminKill(s *bot.Client, m *discord.Message) {
	if m.ReferencedMessage == nil || m.ReferencedMessage.Author.ID == 0 {
		return
	}
	const timeout = 30 * time.Second
	const timeoutReason = "30-second timeout"
	targetID := m.ReferencedMessage.Author.ID.String()
	banner := b.bannerFor(s)
	if err := banner.CanManageMember(m.Author.ID.String(), targetID, "kill"); err != nil {
		b.Logger.Debug("Garmin kill denied", zap.String("caller", m.Author.ID.String()), zap.String("target", targetID), zap.Error(err))
		return
	}
	var err error
	if b.Moderation != nil {
		err = b.Moderation.TimeoutAtLeast(banner, targetID, timeout, timeoutReason)
	} else {
		until := time.Now().Add(timeout)
		err = updateTimeout(s, value(m.GuildID), snowflakeID(targetID), &until)
	}
	if err != nil {
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

func (b *Bot) handleGarminContextResetMessage(s *bot.Client, m *discord.Message) {
	if b.bannerFor(s).HasChannelPermission(m.Author.ID.String(), "ctx-reset", m.ChannelID) != nil {
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
	b.respondDeferredText(s, i, true, "Garmin context reset failed", "Couldn't reset Garmin context.", func() (string, error) {
		if err := b.bannerFor(s).HasChannelPermission(callerID, "ctx-reset", i.Channel().ID()); err != nil {
			return "", err
		}
		if err := b.resetGarminContext(i.Channel().ID().String(), i.ID().String()); err != nil {
			return "", err
		}
		return "Garmin context reset for this channel.", nil
	})
}

func (b *Bot) autoWarnGarminAbuse(s *bot.Client, m *discord.Message, prompt string) bool {
	if !garminDirectSlur(prompt) {
		return false
	}
	reason := "Directed a prohibited slur at Metrobot"
	// The bot is the caller, so native admins and members above the bot stay protected.
	response, extras, _, err := b.Warn.Warn(b.bannerFor(s), discordBotUserID(s).String(), m.Author.ID.String(), reason)
	var denied errDiscordPermission
	if errors.As(err, &denied) {
		return false
	}
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
	{"Notes", "• `/notes` - List all available notes\n• `/note [name]` - Show a specific note\n• `/note [name] [short_desc] [content]` - Add a note (Manage Server)\n• `/addnote [name] [short_desc] [content]` - Add a new note (Manage Server)\n• `/editnote [name] [short_desc] [content]` - Edit a note (Manage Server)\n• `/renamenote [name] [new_name]` - Rename a note (Manage Server)\n• `/delnote [name]` - Delete a note (Manage Server)\n• All saved notes are available in #app-support"},
	{"Bot info", "• `/gsm [search]` - Look up phone specifications on GSMArena\n• `/nanoreview [search]` - Look up devices on NanoReview\n• `/technicalcity [search]` - Look up CPUs and GPUs on Technical City\n• `/gsmcompare`, `/nanoreviewcompare`, `/technicalcitycompare` - Compare two devices on one source\n• `/quote` - Turn the previous message into a quote image\n• `/nuke [user]` or reply `garmin nuke them` - Send a cartoon nuke GIF\n• Message Apps > OCR Translate - Read image text and translate it into English\n• `/version [version]` - Show release info\n• `/latest` - Show the latest release\n• `/actions` - Show GitHub Actions build status\n• `/ping` - Check latency to various services"},
	{"Moderation", "• `/ban [user] [reason]` - Permanently ban a user (Ban Members)\n• `/dban [user] [reason]` - Ban and delete messages\n• `/tban [user] [duration] [reason]` - Temporarily ban a user\n• `/sban [user] [reason]` - Softban a user (Ban Members)\n• `/kick [user] [reason]` - Kick a user (Kick Members)\n• `/timeout [user] [duration] [reason]` - Timeout a user (Moderate Members)\n• `/mute [user] [duration] [reason]` - Timeout alias (Moderate Members)\n• `/warn [user] [reason]` - Warn a user\n• `/warnings [user]` - Show warnings for a user\n• `/unwarn [user] [id]` - Remove a warning from a user\n• `/dehoist [user] [dry]` - Dehoist a user, or omit user to rerun the server (Manage Nicknames)\n• `/approvenick [user]` - Allow a user's next nickname change without dehoisting (Manage Nicknames)\n• `/bulkrole [role] [users]` - Add a lower role to multiple users (Manage Roles required)\n• `/purge [count]` - Delete recent messages (Manage Messages)\n• `/scanreactions` - Scan recent messages for prohibited reactions (Manage Messages)\n\nWarn, unwarn and kill need Moderate Members. Discord Administrator and the server owner have every permission, and the bot will not act on them."},
	{"Fun and Kud", "• `>>kud` - Balance, games played, win rate, payouts, debt, and held stakes\n• `>>daily` - Claim daily ⚜️ Kud every 22h; streak bonuses cap at 10\n• `>>shop` - Kud upgrades: `>>buy swift` shortens cooldowns, `>>buy luck` raises Mines and High-Low rescue chance\n• `>>gc @user` - Give someone a 🍪 cookie, once every 24h\n• `>>lb` - Global ⚜️ Kud leaderboard\n• `>>starlb` - Starboard and sobboard leaderboard\n• `>>slots [bet]` - Slots, default 10 Kud, 60s cooldown\n• `>>bj [bet]` - Blackjack with Hit, Stand, Double Down; 6:5 blackjack payout, 12s cooldown\n• `>>mines [bet] [1..10]` - 4x4 Mines, first pick safe, then cash out or keep picking\n• `>>highlow [bet]` - Guess the next card, Ace high, ties push, cash out your streak\n• `>>fight [bet]` - Open a 50/50 escrow fight; another player matches, winner takes both stakes\n• `>>plinko [amount]` - Each 50 Kud buys one ball, e.g. 500 buys 10. 30s lobby, up to 32 players, one shared GIF showing up to 32 balls; every ball pays independently\n• `>>crash [bet]` or `>>coins-crash [bet]` - Shared 15s lobby, cash out before the crash, up to 32 players, 100x limit\n• `>>loan @lender <amount>` - Ask for a lender-confirmed loan. 20% of incoming rewards repay it. Active debt gives Mines and High-Low a small luck edge\n• `>>wa [question]` - Wolfram|Alpha, with local math\n• `>>rmme [duration] [text]` - Reminder, e.g. `>>rmme 45m pizza out`\n\nBets accept a number, `all`, or `max`, up to 100,000,000 Kud. Missing bets use 10 Kud, or 50 for Plinko. Plinko needs a multiple of 50; `all`/`max` leave any remainder in your balance. Idle solo games cash out after 5 minutes, unplayed games and unmatched fights refund. Restarts refund held stakes. Stats count games settled since this update. Wrap commands in `{{...}}` to run them inside a message."},
	{"Admin and triggers", "**Metrobot AI**\n• `/memory view|append|replace|clear` - View or update shared AI memory (Manage Server)\n• `/ctx-reset`, `ok garmin ctx-reset` or `France Modération ctx-reset` - Forget earlier AI context in this channel (Manage Messages)\n\n**Prefix commands**\nModeration actions can also use `!action [user] [args]`, such as `!ban @user spam`.\n\n**Note triggers**\nType `.notename` to display a note, such as `.help` or `.rules`."},
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
	_, hasShortDesc := opts["short_desc"]
	_, hasContent := opts["content"]
	if hasShortDesc || hasContent {
		if !hasShortDesc || !hasContent {
			respondEphemeral(s, i, "Both short_desc and content are required when adding a note.")
			return
		}
		b.handleAddNote(s, i, opts, callerID)
		return
	}

	deferred := stay
	if deferred {
		if err := deferResponse(s, i, true); err != nil {
			b.Logger.Error("failed to defer note response", zap.Error(err))
			return
		}
		stay = b.bannerFor(s).HasChannelPermission(callerID, "stay", i.Channel().ID()) == nil
	}
	note, err := b.Notes.GetNoteDocument(name)
	if err != nil {
		b.Logger.Error("note error", zap.Error(err))
		if deferred {
			_ = editDeferredResponse(s, i, "Error fetching note.")
		} else {
			respondEphemeral(s, i, "Error fetching note.")
		}
		return
	}

	if deferred {
		// Complete the deferred message before a follow-up can choose its own visibility.
		if err := editDeferredResponse(s, i, "Loading note."); err != nil {
			b.Logger.Error("failed to complete deferred note response", zap.Error(err))
			return
		}
		rendered := note.Render()
		message := authoredNoteMessage(rendered, name, "", -1)
		if message.AllowedMentions == nil {
			message.AllowedMentions = &discord.AllowedMentions{Parse: []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}}
		}
		if !stay {
			message.Flags |= discord.MessageFlagEphemeral
		}
		_, err := s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), message)
		if noteRequestRejected(err) && message.Flags.Has(discord.MessageFlagIsComponentsV2) {
			fallback := newDiscordNoteMessage(rendered.Text, "")
			fallback.AllowedMentions = message.AllowedMentions
			if !stay {
				fallback.Flags |= discord.MessageFlagEphemeral
			}
			_, err = s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), fallback)
		}
		if err != nil {
			b.Logger.Error("failed to send note follow-up", zap.Error(err))
			_ = editDeferredResponse(s, i, "Error sending note.")
			return
		}
		if err := s.Rest.DeleteInteractionResponse(i.ApplicationID(), i.Token()); err != nil {
			b.Logger.Warn("failed to delete deferred note response", zap.Error(err))
		}
		return
	}
	b.respondNote(s, i, name, note.Render(), !stay)
}

func (b *Bot) handleAddNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	b.respondDeferredText(s, i, false, "addnote failed", "Error adding note.", func() (string, error) {
		if err := b.bannerFor(s).HasPermission(callerID, "notes"); err != nil {
			return "", err
		}
		name := opts["name"].String()
		if err := b.Notes.AddNote(name, opts["short_desc"].String(), opts["content"].String()); err != nil {
			return fmt.Sprintf("Error adding note: %s", err), nil
		}
		return fmt.Sprintf("Note `%s` added.", strings.ToLower(name)), nil
	})
}

func (b *Bot) handleEditNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	b.respondDeferredText(s, i, false, "editnote failed", "Error editing note.", func() (string, error) {
		if err := b.bannerFor(s).HasPermission(callerID, "notes"); err != nil {
			return "", err
		}
		name := opts["name"].String()
		if err := b.Notes.EditNote(name, opts["short_desc"].String(), opts["content"].String()); err != nil {
			return fmt.Sprintf("Error editing note: %s", err), nil
		}
		return fmt.Sprintf("Note `%s` updated.", strings.ToLower(name)), nil
	})
}

func (b *Bot) handleRenameNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	b.respondDeferredText(s, i, false, "renamenote failed", "Error renaming note.", func() (string, error) {
		if err := b.bannerFor(s).HasPermission(callerID, "notes"); err != nil {
			return "", err
		}
		name, newName := opts["name"].String(), opts["new_name"].String()
		if err := b.Notes.RenameNote(name, newName); err != nil {
			return fmt.Sprintf("Error renaming note: %s", err), nil
		}
		return fmt.Sprintf("Note `%s` renamed to `%s`.", strings.ToLower(name), strings.ToLower(newName)), nil
	})
}

func (b *Bot) handleDelNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	b.respondDeferredText(s, i, false, "delnote failed", "Error deleting note.", func() (string, error) {
		if err := b.bannerFor(s).HasPermission(callerID, "notes"); err != nil {
			return "", err
		}
		name := opts["name"].String()
		if err := b.Notes.DeleteNote(name); err != nil {
			return fmt.Sprintf("Error deleting note: %s", err), nil
		}
		return fmt.Sprintf("Note `%s` deleted.", strings.ToLower(name)), nil
	})
}

func (b *Bot) handleVersion(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, stay bool) {
	tag := "latest"
	if opt, ok := opts["version"]; ok {
		tag = opt.String()
	}

	b.respondDeferredText(s, i, !stay, "version error", "Error fetching version info.", func() (string, error) {
		if stay {
			if err := b.bannerFor(s).HasChannelPermission(i.User().ID.String(), "stay", i.Channel().ID()); err != nil {
				return "", err
			}
		}
		return b.Version.GetVersion(context.Background(), tag, false)
	})
}

func (b *Bot) handleLatest(s *bot.Client, i *events.ApplicationCommandInteractionCreate, stay bool) {
	b.respondDeferredText(s, i, !stay, "latest error", "Error fetching latest version.", func() (string, error) {
		if stay {
			if err := b.bannerFor(s).HasChannelPermission(i.User().ID.String(), "stay", i.Channel().ID()); err != nil {
				return "", err
			}
		}
		return b.Version.GetVersion(context.Background(), "latest", false)
	})
}

func (b *Bot) handleActions(s *bot.Client, i *events.ApplicationCommandInteractionCreate, stay bool) {
	b.respondDeferredText(s, i, !stay, "actions error", "Error fetching actions status.", func() (string, error) {
		if stay {
			if err := b.bannerFor(s).HasChannelPermission(i.User().ID.String(), "stay", i.Channel().ID()); err != nil {
				return "", err
			}
		}
		return b.Actions.GetActions(context.Background(), false)
	})
}

func (b *Bot) handleBan(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")
	b.respondDeferredText(s, i, false, "ban failed", "Error executing ban.", func() (string, error) {
		resp, _, err := b.Moderation.Ban(b.bannerFor(s), callerID, targetUser.String(), reason)
		return resp, err
	})
}

func (b *Bot) handleDBan(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")
	b.respondDeferredText(s, i, false, "dban failed", "Error executing dban.", func() (string, error) {
		resp, _, err := b.Moderation.DBan(b.bannerFor(s), callerID, targetUser.String(), reason)
		return resp, err
	})
}

func (b *Bot) handleTBan(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	durationStr := opts["duration"].String()
	reason := getOptString(opts, "reason")

	dur, err := util.ParseDuration(durationStr)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("Invalid duration: %s", err))
		return
	}

	b.respondDeferredText(s, i, false, "tban failed", "Error executing tban.", func() (string, error) {
		resp, _, err := b.Moderation.TBan(b.bannerFor(s), callerID, targetUser.String(), dur, reason)
		return resp, err
	})
}

func (b *Bot) handleSBan(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")
	b.respondDeferredText(s, i, false, "sban failed", "Error executing sban.", func() (string, error) {
		resp, _, err := b.Moderation.SBan(b.bannerFor(s), callerID, targetUser.String(), reason)
		return resp, err
	})
}

func (b *Bot) handleKick(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")

	b.respondDeferredText(s, i, false, "kick failed", "Error executing kick.", func() (string, error) {
		resp, _, err := b.Moderation.Kick(b.bannerFor(s), callerID, targetUser.String(), reason)
		return resp, err
	})
}

func (b *Bot) handleMute(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	durationStr := opts["duration"].String()
	reason := getOptString(opts, "reason")

	dur, err := util.ParseDuration(durationStr)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("Invalid duration: %s", err))
		return
	}

	b.respondDeferredText(s, i, false, "mute failed", "Error executing mute.", func() (string, error) {
		resp, _, err := b.Moderation.Mute(b.bannerFor(s), callerID, targetUser.String(), dur, reason)
		return resp, err
	})
}

func (b *Bot) handleWarn(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	reason := getOptString(opts, "reason")
	b.respondDeferredText(s, i, false, "warn failed", "Error executing warn.", func() (string, error) {
		resp, extras, _, err := b.Warn.Warn(b.bannerFor(s), callerID, targetUser.String(), reason)
		if err == nil {
			for _, extra := range extras {
				s.Rest.CreateMessage(i.Channel().ID(), *noPingMessage(suppressDiscordEmbeds(extra)))
			}
		}
		return resp, err
	})
}

func (b *Bot) handleWarnings(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption) {
	targetUser := opts["user"].Snowflake()
	banner := b.bannerFor(s)
	resp, err := b.Warn.Warnings(banner, targetUser.String())
	if err != nil {
		b.Logger.Error("warnings error", zap.Error(err))
		respondEphemeral(s, i, "Error fetching warnings.")
		return
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Flags:           discord.MessageFlagIsComponentsV2,
			Components:      []discord.LayoutComponent{discord.ContainerComponent{Components: []discord.ContainerSubComponent{discord.TextDisplayComponent{Content: truncateRunes(resp, 4000)}}}},
			AllowedMentions: &discord.AllowedMentions{},
		},
	}); err != nil {
		b.Logger.Error("failed to send warnings", zap.Error(err))
	}
}

func (b *Bot) handleUnwarn(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetUser := opts["user"].Snowflake()
	index := int(opts["id"].Int())
	b.respondDeferredText(s, i, false, "unwarn failed", "Error removing warning.", func() (string, error) {
		resp, _, err := b.Warn.Unwarn("discord", callerID, targetUser.String(), index, b.bannerFor(s))
		return resp, err
	})
}

func (b *Bot) handleDehoist(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	dry := getOptBool(opts, "dry")
	var targetID string
	if opt, ok := opts["user"]; ok {
		targetID = opt.Snowflake().String()
	}
	b.respondDeferredText(s, i, dry, "dehoist failed", "Error executing dehoist.", func() (string, error) {
		resp, err := b.Moderation.Dehoist(b.bannerFor(s), callerID, targetID, dry)
		if err != nil {
			return "", err
		}
		if dry && len(resp) > 2000 {
			for _, chunk := range chunkString(resp, 2000) {
				if err := dmUser(s, callerID, chunk); err != nil {
					return "", err
				}
			}
			return "The output is too long, so I sent it to your DMs.", nil
		}
		return resp, nil
	})
}

func (b *Bot) handleApproveNick(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	targetID := opts["user"].Snowflake().String()
	b.respondDeferredText(s, i, true, "failed to approve nickname change", "Could not approve the nickname change.", func() (string, error) {
		if err := b.bannerFor(s).CanManageMember(callerID, targetID, "approvenick"); err != nil {
			return "", err
		}
		if err := b.DB.ApproveNicknameChange(snowflakeString(i.GuildID()), targetID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Approved <@%s>'s next nickname change.", targetID), nil
	})
}

func (b *Bot) handleBulkRole(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Error("failed to defer bulkrole interaction", zap.Error(err))
		return
	}
	banner := b.bannerFor(s)
	guild, err := banner.guild()
	if err != nil {
		_ = editDeferredResponse(s, i, permissionDenial(err))
		return
	}
	caller, found, err := banner.authority(guild, callerID)
	if err != nil {
		_ = editDeferredResponse(s, i, permissionDenial(err))
		return
	}
	if !found || !caller.permissions.Has(discord.PermissionManageRoles) {
		_ = editDeferredResponse(s, i, "You need Manage Roles to use this command.")
		return
	}

	users := parseBulkRoleUsers(opts["users"].String())
	if len(users) == 0 {
		_ = editDeferredResponse(s, i, "Provide at least one username, mention, or ID.")
		return
	}

	roleID := opts["role"].Snowflake()
	var role *discord.Role
	for _, candidate := range guild.Roles {
		if candidate.ID == roleID {
			role = &candidate
			break
		}
	}
	if !canAssignRole(caller, role, guild.ID.String()) {
		_ = editDeferredResponse(s, i, "That role must be editable and below your highest role.")
		return
	}

	members, err := b.bannerFor(s).GetAllMembers()
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

func (b *Bot) handleGarminMemory(s *bot.Client, i *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData, callerID string) {
	if data.SubCommandName == nil {
		respondEphemeral(s, i, "Choose a memory action.")
		return
	}

	if b.garminMemory == nil {
		respondEphemeral(s, i, "Metrobot AI is not configured.")
		return
	}
	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Error("failed to defer memory response", zap.Error(err))
		return
	}
	if err := b.bannerFor(s).HasPermission(callerID, "memory"); err != nil {
		_ = editDeferredResponse(s, i, permissionDenial(err))
		return
	}

	subcommand := *data.SubCommandName
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
			_ = editDeferredResponse(s, i, "-# memory updated\nMetrobot memory updated.")
			return
		}
	case "replace":
		err = b.garminMemory.Replace(getOptString(subopts, "content"))
		if err == nil {
			_ = editDeferredResponse(s, i, "-# memory updated\nMetrobot memory replaced.")
			return
		}
	case "clear":
		err = b.garminMemory.Clear()
		if err == nil {
			_ = editDeferredResponse(s, i, "-# memory updated\nMetrobot memory cleared.")
			return
		}
	default:
		err = fmt.Errorf("unknown memory action %q", subcommand)
	}

	b.Logger.Error("Metrobot memory command failed", zap.String("action", subcommand), zap.Error(err))
	_ = editDeferredResponse(s, i, fmt.Sprintf("Could not manage Metrobot memory: %s", err))
}

func respondGarminMemory(s *bot.Client, i *events.ApplicationCommandInteractionCreate, memory string) error {
	if len(memory) <= 1900 {
		return editDeferredResponse(s, i, "```md\n"+memory+"\n```")
	}
	_, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{
		Content:         ptr("Metrobot's memory is attached."),
		AllowedMentions: &discord.AllowedMentions{},
		Files: []*discord.File{
			{Name: cmd.GarminMemoryFile, Reader: strings.NewReader(memory)},
		},
	})
	return err
}

func (b *Bot) handlePing(s *bot.Client, i *events.ApplicationCommandInteractionCreate) {
	b.respondDeferredText(s, i, false, "ping error", "Error checking ping.", b.Ping.Ping)
}

// respondDeferredText acknowledges the interaction before slow network work,
// so it cannot outlive Discord's three-second window, then edits in the
// result. Errors stay private: a public placeholder is deleted and the error
// is sent as an ephemeral follow-up.
func (b *Bot) respondDeferredText(s *bot.Client, i discord.Interaction, ephemeral bool, logMessage, errorText string, work func() (string, error)) {
	if err := deferResponse(s, i, ephemeral); err != nil {
		b.Logger.Error("failed to defer response", zap.String("context", logMessage), zap.Error(err))
		return
	}
	text, err := work()
	if err != nil {
		var denied errDiscordPermission
		if errors.As(err, &denied) {
			errorText = string(denied)
		} else {
			b.Logger.Error(logMessage, zap.Error(err))
		}
		if ephemeral {
			if err := editDeferredResponse(s, i, errorText); err != nil {
				b.Logger.Error("failed to edit deferred response", zap.Error(err))
			}
			return
		}
		// A follow-up sent directly after a defer inherits its visibility.
		if err := editDeferredResponse(s, i, "The command could not be completed."); err != nil {
			b.Logger.Error("failed to complete deferred response", zap.Error(err))
			return
		}
		if err := s.Rest.DeleteInteractionResponse(i.ApplicationID(), i.Token()); err != nil {
			b.Logger.Warn("failed to delete deferred response", zap.Error(err))
		}
		if _, err := s.Rest.CreateFollowupMessage(i.ApplicationID(), i.Token(), discord.MessageCreate{Content: errorText, Flags: discord.MessageFlagEphemeral, AllowedMentions: &discord.AllowedMentions{}}); err != nil {
			b.Logger.Error("failed to send error follow-up", zap.Error(err))
		}
		return
	}
	message := newDiscordNoteMessage(suppressDiscordEmbeds(text), "")
	mentions := &discord.AllowedMentions{}
	if ephemeral {
		mentions = &discord.AllowedMentions{Parse: []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}}
	}
	if _, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{Content: &message.Content, Files: message.Files, AllowedMentions: mentions}); err != nil {
		b.Logger.Error("failed to edit deferred response", zap.String("context", logMessage), zap.Error(err))
	}
}

func (b *Bot) handlePurge(s *bot.Client, i *events.ApplicationCommandInteractionCreate, opts map[string]discord.SlashCommandOption, callerID string) {
	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Error("failed to defer purge response", zap.Error(err))
		return
	}
	if err := b.bannerFor(s).HasChannelPermission(callerID, "purge", i.Channel().ID()); err != nil {
		_ = editDeferredResponse(s, i, permissionDenial(err))
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

		msgs, err := s.Rest.GetMessages(i.Channel().ID(), 0, 0, 0, int(count))
		if err != nil {
			b.Logger.Error("failed to get messages for purge", zap.Error(err))
			editDeferredResponse(s, i, "Error fetching messages.")
			return
		}

		deleted, failed := b.purgeMessages(s, i.Channel().ID(), msgs)
		resp := fmt.Sprintf("Deleted %d messages.", deleted)
		if failed > 0 {
			resp += fmt.Sprintf(" Could not delete %d.", failed)
		}
		editDeferredResponse(s, i, resp)
	} else {
		_ = editDeferredResponse(s, i, "Please provide a count to purge.")
	}
}

// purgeBulkMaxAge is Discord's age limit for bulk deletion, less an hour of margin.
const purgeBulkMaxAge = 14*24*time.Hour - time.Hour

// purgeMessages deletes msgs and counts only deletions Discord confirmed.
// Messages too old for bulk deletion, and any batch the bulk call rejects,
// fall back to single deletes.
func (b *Bot) purgeMessages(s *bot.Client, channelID snowflake.ID, msgs []discord.Message) (deleted, failed int) {
	var recent, single []snowflake.ID
	for _, msg := range msgs {
		if time.Since(msg.ID.Time()) < purgeBulkMaxAge {
			recent = append(recent, msg.ID)
		} else {
			single = append(single, msg.ID)
		}
	}
	if len(recent) > 1 {
		if err := s.Rest.BulkDeleteMessages(channelID, recent); err != nil {
			b.Logger.Warn("bulk purge failed; deleting one by one", zap.Error(err))
			single = append(single, recent...)
		} else {
			deleted += len(recent)
		}
	} else {
		single = append(single, recent...)
	}
	for _, id := range single {
		if err := s.Rest.DeleteMessage(channelID, id); err != nil {
			if !isDiscordNotFound(err) {
				b.Logger.Warn("failed to purge message", zap.String("message", id.String()), zap.Error(err))
				failed++
			}
			continue
		}
		deleted++
	}
	return deleted, failed
}

func (b *Bot) handleScanReactions(s *bot.Client, i *events.ApplicationCommandInteractionCreate, callerID string) {
	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Error("failed to defer scanreactions response", zap.Error(err))
		return
	}
	if err := b.bannerFor(s).HasChannelPermission(callerID, "scanreactions", i.Channel().ID()); err != nil {
		_ = editDeferredResponse(s, i, permissionDenial(err))
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
	// Defer the response since this might take time
	if err := deferResponse(s, i, true); err != nil {
		b.Logger.Error("failed to defer refreshstarboard response", zap.Error(err))
		return
	}
	if err := b.bannerFor(s).HasPermission(callerID, "refreshstarboard"); err != nil {
		_ = editDeferredResponse(s, i, permissionDenial(err))
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

func canAssignRole(member discordAuthority, role *discord.Role, guildID string) bool {
	if role == nil || role.Managed || role.ID.String() == guildID || !member.permissions.Has(discord.PermissionManageRoles) {
		return false
	}
	return member.above(discordAuthority{top: role.Position, topID: role.ID})
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
	blockedGuildID := b.discordSettings().Moderation.BlockedGuildID
	tag := member.User.PrimaryGuild
	if tag == nil || !value(tag.IdentityEnabled) || !configuredID(blockedGuildID, snowflakeString(tag.IdentityGuildID)) {
		return
	}
	reason := "Displayed a blocked server tag (guild " + blockedGuildID + ")"
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
	welcome := b.discordSettings().Welcome
	if roleID := snowflakeID(welcome.RoleID); roleID != 0 && snowflakeID(welcome.ChannelID) != 0 && before.User.ID != 0 && !slices.Contains(before.RoleIDs, roleID) && slices.Contains(member.RoleIDs, roleID) {
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
	welcome := b.discordSettings().Welcome
	welcomeChannelID := snowflakeID(welcome.ChannelID)
	if welcomeChannelID == 0 {
		return
	}
	note, err := b.Notes.GetNoteDocument(welcome.Note)
	if err != nil {
		// Retire tester onboarding while retaining a thank-you for new donors.
		_, sendErr := s.Rest.CreateMessage(welcomeChannelID, discord.MessageCreate{
			Content:         fmt.Sprintf("<@%s>\nThank you for supporting Metrolist. Metrolist v14 is free and public for everyone.", userID),
			AllowedMentions: &discord.AllowedMentions{Users: []snowflake.ID{snowflakeID(userID)}},
		})
		if sendErr != nil {
			b.Logger.Error("failed to send donor thank-you", zap.Error(sendErr))
		}
		return
	}
	rendered := note.Render()
	prefix := fmt.Sprintf("<@%s>\n", userID)
	message := authoredNoteMessage(rendered, welcome.Note, prefix, -1)
	message.AllowedMentions = &discord.AllowedMentions{Users: []snowflake.ID{snowflakeID(userID)}}
	_, err = s.Rest.CreateMessage(welcomeChannelID, message)
	if noteRequestRejected(err) && message.Flags.Has(discord.MessageFlagIsComponentsV2) {
		fallback := newDiscordNoteMessage(rendered.Text, prefix)
		fallback.AllowedMentions = message.AllowedMentions
		_, err = s.Rest.CreateMessage(welcomeChannelID, fallback)
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
	if targetMember.User.Bot {
		return true
	}

	if !canManageMember(s, guildID, botMember, targetMember) {
		b.Logger.Debug("skipping auto-dehoist - insufficient permissions",
			zap.String("userID", userID), zap.String("source", source), zap.Int("attempt", attempt), zap.String("displayName", value(targetMember.Nick)))
		return false
	}

	banner := b.bannerFor(s)
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

		_, err := b.Moderation.Dehoist(banner, discordBotUserID(s).String(), userID, false)
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
