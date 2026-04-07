package telegram

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/MetrolistGroup/metrobot/util"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

var chatModPattern = regexp.MustCompile(`(?i)^!(ban|dban|tban|sban|mute|warn)\s*(.*)$`)
var telegramBoldPattern = regexp.MustCompile(`\*\*([^*\n][^\n]*?)\*\*`)
var telegramInlineCodePattern = regexp.MustCompile("`([^`\\n]+)`")

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	callerID := telegramSenderID(msg)

	if msg.IsCommand() {
		b.handleCommand(msg, callerID)
		return
	}

	content := strings.TrimSpace(msg.Text)

	// Check for "Ok Garmin" trigger (case insensitive, comma optional)
	content = b.garminProcessor.ProcessTrigger(content)

	if noteName := extractTriggeredNoteName(content, b.API.Self.UserName); noteName != "" {
		// Log note triggering since it's an action
		b.Logger.Info("note triggered",
			zap.String("user", callerID),
			zap.String("note", noteName),
		)
		text, err := b.Notes.GetNote(noteName)
		if err != nil {
			b.Logger.Error("note lookup error", zap.Error(err))
			return
		}
		sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, formatTelegramNoteHTML(text), "HTML", false, b.Logger)
		return
	}

	matches := chatModPattern.FindStringSubmatch(content)
	if matches == nil {
		// No action triggered, so don't log this message
		return
	}

	// Log moderation command attempt since it's an action
	action := strings.ToLower(matches[1])
	b.Logger.Info("moderation command attempted",
		zap.String("user", callerID),
		zap.String("action", action),
	)

	args := strings.TrimSpace(matches[2])

	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		return
	}

	if args == "" {
		usageMap := map[string]string{
			"ban":  "ban - usage: ban [user] [reason]",
			"dban": "dban - usage: dban [user] [reason]",
			"tban": "tban - usage: tban [user] [duration] [reason]",
			"sban": "sban - usage: sban [user] [reason]",
			"warn": "warn - usage: warn [user] [reason]",
		}
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, usageMap[action], "", false, b.Logger)
		return
	}

	var targetID string
	var commandArgs string

	// Check if this is a reply
	if msg.ReplyToMessage != nil {
		// Use the replied-to user as target
		targetID = extractTelegramUserID(msg, "")
		// Full args string is the reason
		commandArgs = args
	} else {
		// Normal mode: first arg is target, rest is reason
		parts := strings.Fields(args)
		if len(parts) == 0 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Please specify a target user or reply to a message.", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, parts[0])
		if len(parts) > 1 {
			commandArgs = strings.Join(parts[1:], " ")
		}
	}

	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve target user.", "", false, b.Logger)
		return
	}

	if b.DB.IsAdmin("telegram", targetID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "I will not take action against an admin.", "", false, b.Logger)
		return
	}

	// Request confirmation before executing prefix command
	b.requestPrefixConfirmation(msg, action, commandArgs, targetID)
}

func (b *Bot) handleCommand(msg *tgbotapi.Message, callerID string) {
	command := msg.Command()
	args := msg.CommandArguments()
	stay := b.DB.IsAdmin("telegram", callerID, b.Config) && strings.Contains(args, " stay")
	if stay {
		args = strings.Replace(args, " stay", "", 1)
		args = strings.TrimSpace(args)
	}

	b.Logger.Info("command",
		zap.String("command", command),
		zap.String("user", callerID),
		zap.String("args", args),
	)

	switch command {
	case "help":
		b.tgHandleHelp(msg)
	case "notes":
		b.tgHandleNotes(msg, stay)
	case "note":
		b.tgHandleNote(msg, args, stay)
	case "addnote":
		b.tgHandleAddNote(msg, args, callerID)
	case "editnote":
		b.tgHandleEditNote(msg, args, callerID)
	case "delnote":
		b.tgHandleDelNote(msg, args, callerID)
	case "version":
		b.tgHandleVersion(msg, args, stay)
	case "latest":
		b.tgHandleLatest(msg, stay)
	case "actions":
		b.tgHandleActions(msg, stay)
	case "ban":
		b.tgHandleBan(msg, args, callerID)
	case "dban":
		b.tgHandleDBan(msg, args, callerID)
	case "tban":
		b.tgHandleTBan(msg, args, callerID)
	case "sban":
		b.tgHandleSBan(msg, args, callerID)
	case "mute":
		b.tgHandleMute(msg, args, callerID)
	case "warn":
		b.tgHandleWarn(msg, args, callerID)
	case "warnings":
		b.tgHandleWarnings(msg, args)
	case "warns":
		b.tgHandleWarnings(msg, args)
		b.tgHandleWarnings(msg, args)
	case "unwarn":
		b.tgHandleUnwarn(msg, args, callerID)
	case "dehoist":
		b.tgHandleDehoist(msg, args, callerID)
	case "addadmin":
		b.tgHandleAddAdmin(msg, args, callerID)
	case "removeadmin":
		b.tgHandleRemoveAdmin(msg, args, callerID)
	case "ping":
		b.tgHandlePing(msg)
	}
}

func (b *Bot) tgHandleHelp(msg *tgbotapi.Message) {
	help := `<b>Available Commands:</b>

<b>Notes:</b>
• /notes - List all available notes
• /note - Show a specific note
• /addnote - Add a new note (admin only)
• /editnote - Edit a note (admin only)
• /delnote - Delete a note (admin only)

<b>Bot Info:</b>
• /version - Show release info
• /latest - Show the latest release
• /actions - GitHub Actions build status
• /ping - Check latency to services

<b>Moderation (admin only):</b>
• /ban - Permanently ban a user
• /dban - Ban and delete messages
• /tban - Temporarily ban a user
• /sban - Softban a user
• /mute - Mute a user
• /warn - Warn a user
• /warnings - Show warnings for a user
• /warns - Show warnings (alias)
• /unwarn - Remove a warning from a user
• /dehoist - Remove hoisting characters from name

<b>Admin Management (permaadmin only):</b>
• /addadmin - Add a bot admin
• /removeadmin - Remove a bot admin

<b>Prefix Commands:</b>
Moderation actions can also be triggered via message prefix: !<code>action</code> [user] [args]
Example: <code>!ban @user spam</code>`
	sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, help, "HTML", false, b.Logger)
}

func (b *Bot) tgHandleNotes(msg *tgbotapi.Message, stay bool) {
	text, err := b.Notes.ListNotes()
	if err != nil {
		b.Logger.Error("notes error", zap.Error(err))
		return
	}
	sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, formatTelegramNoteHTML(text), "HTML", stay, b.Logger)
}

func (b *Bot) tgHandleNote(msg *tgbotapi.Message, args string, stay bool) {
	name := strings.TrimSpace(args)
	if name == "" {
		sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, "Usage: /note [name]", "", stay, b.Logger)
		return
	}
	text, err := b.Notes.GetNote(name)
	if err != nil {
		b.Logger.Error("note error", zap.Error(err))
		return
	}
	sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, formatTelegramNoteHTML(text), "HTML", stay, b.Logger)
}

func (b *Bot) tgHandleAddNote(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Only admins can add notes.", "", false, b.Logger)
		return
	}
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 2 {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Usage: /addnote [name] [content]", "", false, b.Logger)
		return
	}
	if err := b.Notes.AddNote(parts[0], parts[1]); err != nil {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Error: %s", err), "", false, b.Logger)
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Note <code>%s</code> added.", html.EscapeString(strings.ToLower(parts[0]))), "HTML", false, b.Logger)
}

func (b *Bot) tgHandleEditNote(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Only admins can edit notes.", "", false, b.Logger)
		return
	}
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 2 {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Usage: /editnote [name] [content]", "", false, b.Logger)
		return
	}
	if err := b.Notes.EditNote(parts[0], parts[1]); err != nil {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Error: %s", err), "", false, b.Logger)
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Note <code>%s</code> updated.", html.EscapeString(strings.ToLower(parts[0]))), "HTML", false, b.Logger)
}

func (b *Bot) tgHandleDelNote(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Only admins can delete notes.", "", false, b.Logger)
		return
	}
	name := strings.TrimSpace(args)
	if name == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Usage: /delnote [name]", "", false, b.Logger)
		return
	}
	if err := b.Notes.DeleteNote(name); err != nil {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Error: %s", err), "", false, b.Logger)
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Note <code>%s</code> deleted.", html.EscapeString(strings.ToLower(name))), "HTML", false, b.Logger)
}

func (b *Bot) tgHandleVersion(msg *tgbotapi.Message, args string, stay bool) {
	tag := strings.TrimSpace(args)
	if tag == "" {
		tag = "latest"
	}
	text, err := b.Version.GetVersion(context.Background(), tag, true)
	if err != nil {
		b.Logger.Error("version error", zap.Error(err))
		return
	}
	sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, text, "HTML", stay, b.Logger)
}

func (b *Bot) tgHandleLatest(msg *tgbotapi.Message, stay bool) {
	text, err := b.Version.GetVersion(context.Background(), "latest", true)
	if err != nil {
		b.Logger.Error("latest error", zap.Error(err))
		return
	}
	sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, text, "HTML", stay, b.Logger)
}

func (b *Bot) tgHandleActions(msg *tgbotapi.Message, stay bool) {
	text, err := b.Actions.GetActions(context.Background(), true)
	if err != nil {
		b.Logger.Error("actions error", zap.Error(err))
		return
	}
	sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, text, "HTML", stay, b.Logger)
}

func (b *Bot) tgHandleBan(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "You don't have ban permissions.", "", false, b.Logger)
		return
	}
	parts := strings.Fields(args)
	var targetID string
	var reason string

	if msg.ReplyToMessage != nil {
		targetID = extractTelegramUserID(msg, "")
		reason = strings.TrimSpace(args)
	} else {
		if len(parts) == 0 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "ban - usage: ban [user] [reason]", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, parts[0])
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
	}

	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	if reason == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "ban - usage: ban [user] [reason]", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, _, err := b.Moderation.Ban(banner, callerID, targetID, reason, b.Config)
	if err != nil {
		b.Logger.Error("ban failed", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandleDBan(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "You don't have ban permissions.", "", false, b.Logger)
		return
	}
	parts := strings.Fields(args)
	var targetID string
	var reason string

	if msg.ReplyToMessage != nil {
		targetID = extractTelegramUserID(msg, "")
		reason = strings.TrimSpace(args)
	} else {
		if len(parts) == 0 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "dban - usage: dban [user] [reason]", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, parts[0])
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
	}

	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	if reason == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "dban - usage: dban [user] [reason]", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, _, err := b.Moderation.DBan(banner, callerID, targetID, reason, b.Config)
	if err != nil {
		b.Logger.Error("dban failed", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandleTBan(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "You don't have ban permissions.", "", false, b.Logger)
		return
	}
	parts := strings.Fields(args)
	var targetID string

	if msg.ReplyToMessage != nil {
		if len(parts) < 2 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "tban - usage: tban [user] [duration] [reason]", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, "")
	} else {
		if len(parts) < 2 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "tban - usage: tban [user] [duration] [reason]", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, parts[0])
	}

	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	dur, err := util.ParseDuration(parts[1])
	if err != nil {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Invalid duration: %s", err), "", false, b.Logger)
		return
	}
	var reason string
	if len(parts) > 2 {
		reason = strings.Join(parts[2:], " ")
	}
	if reason == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "tban - usage: tban [user] [duration] [reason]", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, _, err := b.Moderation.TBan(banner, callerID, targetID, dur, reason, b.Config)
	if err != nil {
		b.Logger.Error("tban failed", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandleSBan(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "You don't have ban permissions.", "", false, b.Logger)
		return
	}
	parts := strings.Fields(args)
	var targetID string
	var reason string

	if msg.ReplyToMessage != nil {
		targetID = extractTelegramUserID(msg, "")
		reason = strings.TrimSpace(args)
	} else {
		if len(parts) == 0 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "sban - usage: sban [user] [reason]", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, parts[0])
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
	}

	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	if reason == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "sban - usage: sban [user] [reason]", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, _, err := b.Moderation.SBan(banner, callerID, targetID, reason, b.Config)
	if err != nil {
		b.Logger.Error("sban failed", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandleMute(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "You don't have mute permissions.", "", false, b.Logger)
		return
	}
	parts := strings.Fields(args)
	var targetID string
	var reason string

	if msg.ReplyToMessage != nil {
		if len(parts) < 2 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "mute - usage: mute [user] [duration] [reason]", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, "")
	} else {
		if len(parts) < 2 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "mute - usage: mute [user] [duration] [reason]", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, parts[0])
	}

	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	dur, err := util.ParseDuration(parts[1])
	if err != nil {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Invalid duration: %s", err), "", false, b.Logger)
		return
	}
	if len(parts) > 2 {
		reason = strings.Join(parts[2:], " ")
	}
	if reason == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "mute - usage: mute [user] [duration] [reason]", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, _, err := b.Moderation.Mute(banner, callerID, targetID, dur, reason, b.Config)
	if err != nil {
		b.Logger.Error("mute failed", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandleWarn(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "You don't have warn permissions.", "", false, b.Logger)
		return
	}
	parts := strings.Fields(args)

	// Support two forms:
	// 1) Reply + /warn [reason...]  -> target is reply user, reason is whole args
	// 2) /warn [user] [reason...]  -> target from first arg, reason from the rest
	var targetID string
	var reason string

	if msg.ReplyToMessage != nil {
		targetID = extractTelegramUserID(msg, "")
		reason = strings.TrimSpace(args)
	} else {
		if len(parts) == 0 {
			sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "warn - usage: warn [user] [reason]", "", false, b.Logger)
			return
		}
		targetID = extractTelegramUserID(msg, parts[0])
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
	}

	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	if reason == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "warn - usage: warn [user] [reason]", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, extras, _, err := b.Warn.Warn(banner, callerID, targetID, reason, b.Config)
	if err != nil {
		b.Logger.Error("warn failed", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
	for _, extra := range extras {
		reply := tgbotapi.NewMessage(msg.Chat.ID, extra)
		reply.DisableWebPagePreview = true
		b.API.Send(reply)
	}
}

func (b *Bot) tgHandleWarnings(msg *tgbotapi.Message, args string) {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Usage: /warnings [user]", "", false, b.Logger)
		return
	}
	targetID := extractTelegramUserID(msg, parts[0])
	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, err := b.Warn.Warnings(banner, targetID)
	if err != nil {
		b.Logger.Error("warnings error", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandleUnwarn(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "You don't have unwarn permissions.", "", false, b.Logger)
		return
	}
	parts := strings.Fields(args)
	if len(parts) < 2 {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Usage: /unwarn [user] [id]", "", false, b.Logger)
		return
	}
	targetID := extractTelegramUserID(msg, parts[0])
	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Invalid warning index.", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, _, err := b.Warn.Unwarn("telegram", callerID, targetID, index, banner)
	if err != nil {
		b.Logger.Error("unwarn error", zap.Error(err))
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Error: %s", err), "", false, b.Logger)
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandleDehoist(msg *tgbotapi.Message, args string, callerID string) {
	if !b.DB.IsAdmin("telegram", callerID, b.Config) {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "You don't have dehoist permissions.", "", false, b.Logger)
		return
	}

	parts := strings.Fields(args)
	dry := false
	var targetID string
	for _, p := range parts {
		if strings.ToLower(p) == "dry" {
			dry = true
		} else if targetID == "" {
			targetID = extractTelegramUserID(msg, p)
		}
	}

	banner := b.newBanner()
	resp, err := b.Moderation.Dehoist(banner, targetID, dry, b.Config)
	if err != nil {
		b.Logger.Error("dehoist error", zap.Error(err))
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Error: %s", err), "", false, b.Logger)
		return
	}

	if dry && len(resp) > 4096 {
		chunks := chunkString(resp, 4096)
		uid, _ := strconv.ParseInt(callerID, 10, 64)
		for _, chunk := range chunks {
			dmUser(b.API, uid, chunk)
		}
		sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, "Output too large - sent to your DMs.", "", false, b.Logger)
		return
	}

	if dry {
		sendEphemeralReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
	} else {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
	}
}

func (b *Bot) tgHandleAddAdmin(msg *tgbotapi.Message, args string, callerID string) {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Usage: /addadmin [user]", "", false, b.Logger)
		return
	}
	targetID := extractTelegramUserID(msg, parts[0])
	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, err := b.Admin.AddAdmin(banner, callerID, targetID, b.Config)
	if err != nil {
		b.Logger.Error("addadmin error", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandleRemoveAdmin(msg *tgbotapi.Message, args string, callerID string) {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Usage: /removeadmin [user]", "", false, b.Logger)
		return
	}
	targetID := extractTelegramUserID(msg, parts[0])
	if targetID == "" {
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Could not resolve user.", "", false, b.Logger)
		return
	}
	banner := b.newBanner()
	resp, err := b.Admin.RemoveAdmin(banner, callerID, targetID, b.Config)
	if err != nil {
		b.Logger.Error("removeadmin error", zap.Error(err))
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, resp, "", false, b.Logger)
}

func (b *Bot) tgHandlePing(msg *tgbotapi.Message) {
	text, err := b.Ping.Ping()
	if err != nil {
		b.Logger.Error("ping error", zap.Error(err))
		sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, "Error checking ping.", "", false, b.Logger)
		return
	}
	sendPublicReply(b.API, msg.Chat.ID, msg.MessageID, text, "", false, b.Logger)
}

func extractTelegramUserID(msg *tgbotapi.Message, mention string) string {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		return strconv.FormatInt(msg.ReplyToMessage.From.ID, 10)
	}

	mention = strings.TrimPrefix(mention, "@")

	if id, err := strconv.ParseInt(mention, 10, 64); err == nil {
		return strconv.FormatInt(id, 10)
	}

	if msg.Entities != nil {
		for _, entity := range msg.Entities {
			if entity.Type == "text_mention" && entity.User != nil {
				return strconv.FormatInt(entity.User.ID, 10)
			}
		}
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

func telegramSenderID(msg *tgbotapi.Message) string {
	if msg.From != nil {
		return strconv.FormatInt(msg.From.ID, 10)
	}
	if msg.SenderChat != nil {
		return strconv.FormatInt(msg.SenderChat.ID, 10)
	}
	return "0"
}

func extractTriggeredNoteName(content, botUsername string) string {
	if !strings.HasPrefix(content, "#") {
		return ""
	}

	// Count leading hash symbols
	i := 0
	for i < len(content) && content[i] == '#' {
		i++
	}

	if i >= len(content) {
		return "" // Only hash symbols, no note name
	}

	// If there's whitespace immediately after hashes, it's not a valid note
	if content[i] == ' ' || content[i] == '\t' {
		return ""
	}

	// Extract the first word as note name
	remainder := content[i:]
	fields := strings.Fields(remainder)
	if len(fields) == 0 {
		return ""
	}

	noteName := fields[0]
	if botUsername != "" {
		suffix := "@" + strings.ToLower(botUsername)
		lowerName := strings.ToLower(noteName)
		if strings.HasSuffix(lowerName, suffix) && len(noteName) > len(suffix) {
			noteName = noteName[:len(noteName)-len(suffix)]
		}
	}

	return noteName
}

func formatTelegramNoteHTML(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	formatted := make([]string, 0, len(lines))

	for _, line := range lines {
		formatted = append(formatted, formatTelegramNoteLine(line))
	}

	return strings.Join(formatted, "\n")
}

func formatTelegramNoteLine(line string) string {
	trimmed := strings.TrimSpace(line)

	switch {
	case strings.HasPrefix(trimmed, "## "):
		return "<b>" + formatTelegramInlineHTML(strings.TrimSpace(trimmed[3:])) + "</b>"
	case strings.HasPrefix(trimmed, "# "):
		return "<b>" + formatTelegramInlineHTML(strings.TrimSpace(trimmed[2:])) + "</b>"
	case strings.HasPrefix(trimmed, "- "):
		return "• " + formatTelegramInlineHTML(strings.TrimSpace(trimmed[2:]))
	case strings.HasPrefix(trimmed, "* "):
		return "• " + formatTelegramInlineHTML(strings.TrimSpace(trimmed[2:]))
	default:
		return formatTelegramInlineHTML(line)
	}
}

func formatTelegramInlineHTML(text string) string {
	escaped := html.EscapeString(text)
	protected, codePlaceholders := protectTelegramCodeSpans(escaped)
	protected = telegramBoldPattern.ReplaceAllString(protected, "<b>$1</b>")
	for placeholder, codeHTML := range codePlaceholders {
		protected = strings.ReplaceAll(protected, placeholder, codeHTML)
	}
	return protected
}

func protectTelegramCodeSpans(text string) (string, map[string]string) {
	placeholders := make(map[string]string)
	index := 0

	protected := telegramInlineCodePattern.ReplaceAllStringFunc(text, func(match string) string {
		placeholder := fmt.Sprintf("__METROBOT_CODE_%d__", index)
		placeholders[placeholder] = telegramInlineCodePattern.ReplaceAllString(match, "<code>$1</code>")
		index++
		return placeholder
	})

	return protected, placeholders
}
