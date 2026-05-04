package discord

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/util"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

var chatModPattern = regexp.MustCompile(`(?i)^!(ban|dban|tban|sban|mute|warn)\s*(.*)$`)

func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID != b.Config.DiscordGuildID {
		return
	}

	if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
		b.handleAutocomplete(s, i)
		return
	}

	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	callerID := i.Member.User.ID
	opts := optionMap(data.Options)

	b.Logger.Info("slash command",
		zap.String("command", data.Name),
		zap.String("user", callerID),
	)

	stay := getOptBool(opts, "stay") && b.DB.IsAdmin("discord", callerID, b.Config)

	switch data.Name {
	case "help":
		b.handleHelp(s, i)
	case "notes":
		b.handleNotes(s, i)
	case "note":
		b.handleNote(s, i, opts, stay)
	case "addnote":
		b.handleAddNote(s, i, opts, callerID)
	case "editnote":
		b.handleEditNote(s, i, opts, callerID)
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
	case "mute":
		b.handleMute(s, i, opts, callerID)
	case "warn":
		b.handleWarn(s, i, opts, callerID)
	case "warnings":
		b.handleWarnings(s, i, opts)
	case "unwarn":
		b.handleUnwarn(s, i, opts, callerID)
	case "dehoist":
		b.handleDehoist(s, i, opts, callerID)
	case "addadmin":
		b.handleAddAdmin(s, i, opts, callerID)
	case "removeadmin":
		b.handleRemoveAdmin(s, i, opts, callerID)
	case "ping":
		b.handlePing(s, i)
	case "purge":
		b.handlePurge(s, i, opts, callerID)
	case "refreshstarboard":
		b.handleRefreshStarboard(s, i, callerID)
	}
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot || m.GuildID != b.Config.DiscordGuildID {
		return
	}

	content := strings.TrimSpace(m.Content)

	// Check for "Ok Garmin" trigger (case insensitive, comma optional)
	content = b.garminProcessor.ProcessTrigger(content)

	if noteName := extractNoteName(content); noteName != "" {
		text, err := b.Notes.GetNote(noteName)
		if err != nil {
			b.Logger.Debug("note not found", zap.String("note", noteName), zap.Error(err))
			return
		}
		sendReply(s, m.ChannelID, m.ID, text, false, b.Logger)
		return
	}

	matches := chatModPattern.FindStringSubmatch(content)
	if matches == nil {
		return
	}

	action := strings.ToLower(matches[1])
	args := strings.TrimSpace(matches[2])
	callerID := m.Author.ID

	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		return
	}

	if args == "" {
		usageMap := map[string]string{
			"ban":  "ban - usage: ban [user] [reason]",
			"dban": "dban - usage: dban [user] [reason]",
			"tban": "tban - usage: tban [user] [duration] [reason]",
			"sban": "sban - usage: sban [user] [reason]",
			"mute": "mute - usage: mute [user] [duration] [reason]",
			"warn": "warn - usage: warn [user] [reason]",
		}
		sendReply(s, m.ChannelID, m.ID, usageMap[action], false, b.Logger)
		return
	}

	var targetID string
	var commandArgs string

	// Check if this is a reply
	if m.ReferencedMessage != nil {
		// Use the replied-to user as target
		targetID = m.ReferencedMessage.Author.ID
		// Full args string is the reason
		commandArgs = args
	} else {
		// Normal mode: first arg is target, rest is reason
		parts := strings.Fields(args)
		if len(parts) == 0 {
			sendReply(s, m.ChannelID, m.ID, "Please specify a target user or reply to a message.", false, b.Logger)
			return
		}
		targetID = extractUserID(parts[0])
		if len(parts) > 1 {
			commandArgs = strings.Join(parts[1:], " ")
		}
	}

	if targetID == "" {
		sendReply(s, m.ChannelID, m.ID, "Could not resolve target user.", false, b.Logger)
		return
	}

	if b.DB.IsAdmin("discord", targetID, b.Config) {
		sendReply(s, m.ChannelID, m.ID, "I will not take action against an admin.", false, b.Logger)
		return
	}

	// Execute the command directly
	b.executePrefixCommand(s, m.ChannelID, m.Author.ID, action, commandArgs, targetID)
}

// --- Slash command handlers ---

func (b *Bot) handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	help := "**Available Commands:**\n\n" +
		"**Notes:**\n" +
		"• /notes - List all available notes\n" +
		"• /note [name] - Show a specific note\n" +
		"• /addnote [name] [content] - Add a new note (admin only)\n" +
		"• /editnote [name] [content] - Edit a note (admin only)\n" +
		"• /delnote [name] - Delete a note (admin only)\n\n" +
		"**Bot Info:**\n" +
		"• /version [version] - Show release info\n" +
		"• /latest - Show the latest release\n" +
		"• /actions - Show GitHub Actions build status\n" +
		"• /ping - Check latency to various services\n\n" +
		"**Moderation (admin only):**\n" +
		"• /ban [user] [reason] - Permanently ban a user\n" +
		"• /dban [user] [reason] - Ban and delete messages\n" +
		"• /tban [user] [duration] [reason] - Temporarily ban a user\n" +
		"• /sban [user] [reason] - Softban a user\n" +
		"• /mute [user] [duration] [reason] - Mute a user\n" +
		"• /warn [user] [reason] - Warn a user\n" +
		"• /warnings [user] - Show warnings for a user\n" +
		"• /unwarn [user] [id] - Remove a warning from a user\n" +
		"• /dehoist [user] [dry] - Remove hoisting characters from name\n" +
		"• /purge [count] - Delete messages (reply to message or use count)\n\n" +
		"**Admin Management (permaadmin only):**\n" +
		"• /addadmin [user] - Add a bot admin\n" +
		"• /removeadmin [user] - Remove a bot admin\n\n" +
		"**Prefix Commands:**\n" +
		"Moderation actions can also be triggered via message prefix: !action [user] [args]\n" +
		"Example: !ban @user spam\n\n" +
		"**Notes Trigger:**\n" +
		"Type nnotename to display a note (e.g., nhelp, nrules)"
	respondEphemeral(s, i, help)
}

func (b *Bot) handleNotes(s *discordgo.Session, i *discordgo.InteractionCreate) {
	text, err := b.Notes.ListNotes()
	if err != nil {
		b.Logger.Error("notes error", zap.Error(err))
		respondEphemeral(s, i, "Error listing notes.")
		return
	}
	respondEphemeral(s, i, text)
}

func (b *Bot) handleNote(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, stay bool) {
	name := opts["name"].StringValue()
	text, err := b.Notes.GetNote(name)
	if err != nil {
		b.Logger.Error("note error", zap.Error(err))
		respondEphemeral(s, i, "Error fetching note.")
		return
	}

	if stay {
		respondPublic(s, i, text)
	} else {
		respondPublic(s, i, text)
	}
}

func (b *Bot) handleAddNote(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can add notes.")
		return
	}
	name := opts["name"].StringValue()
	content := opts["content"].StringValue()
	if err := b.Notes.AddNote(name, content); err != nil {
		respondPublic(s, i, fmt.Sprintf("Error adding note: %s", err))
		return
	}
	respondPublic(s, i, fmt.Sprintf("Note `%s` added.", strings.ToLower(name)))
}

func (b *Bot) handleEditNote(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can edit notes.")
		return
	}
	name := opts["name"].StringValue()
	content := opts["content"].StringValue()
	if err := b.Notes.EditNote(name, content); err != nil {
		respondPublic(s, i, fmt.Sprintf("Error editing note: %s", err))
		return
	}
	respondPublic(s, i, fmt.Sprintf("Note `%s` updated.", strings.ToLower(name)))
}

func (b *Bot) handleDelNote(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "Only admins can delete notes.")
		return
	}
	name := opts["name"].StringValue()
	if err := b.Notes.DeleteNote(name); err != nil {
		respondPublic(s, i, fmt.Sprintf("Error deleting note: %s", err))
		return
	}
	respondPublic(s, i, fmt.Sprintf("Note `%s` deleted.", strings.ToLower(name)))
}

func (b *Bot) handleVersion(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, stay bool) {
	tag := "latest"
	if opt, ok := opts["version"]; ok {
		tag = opt.StringValue()
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

func (b *Bot) handleLatest(s *discordgo.Session, i *discordgo.InteractionCreate, stay bool) {
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

func (b *Bot) handleActions(s *discordgo.Session, i *discordgo.InteractionCreate, stay bool) {
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

func (b *Bot) handleBan(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have ban permissions.")
		return
	}
	targetUser := opts["user"].UserValue(s)
	reason := getOptString(opts, "reason")
	banner := b.newBanner()

	resp, _, err := b.Moderation.Ban(banner, callerID, targetUser.ID, reason, b.Config)
	if err != nil {
		b.Logger.Error("ban failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing ban.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleDBan(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have ban permissions.")
		return
	}
	targetUser := opts["user"].UserValue(s)
	reason := getOptString(opts, "reason")
	banner := b.newBanner()

	resp, _, err := b.Moderation.DBan(banner, callerID, targetUser.ID, reason, b.Config)
	if err != nil {
		b.Logger.Error("dban failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing dban.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleTBan(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have ban permissions.")
		return
	}
	targetUser := opts["user"].UserValue(s)
	durationStr := opts["duration"].StringValue()
	reason := getOptString(opts, "reason")

	dur, err := util.ParseDuration(durationStr)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("Invalid duration: %s", err))
		return
	}

	banner := b.newBanner()
	resp, _, err := b.Moderation.TBan(banner, callerID, targetUser.ID, dur, reason, b.Config)
	if err != nil {
		b.Logger.Error("tban failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing tban.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleSBan(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have ban permissions.")
		return
	}
	targetUser := opts["user"].UserValue(s)
	reason := getOptString(opts, "reason")
	banner := b.newBanner()

	resp, _, err := b.Moderation.SBan(banner, callerID, targetUser.ID, reason, b.Config)
	if err != nil {
		b.Logger.Error("sban failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing sban.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleMute(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have mute permissions.")
		return
	}
	targetUser := opts["user"].UserValue(s)
	durationStr := opts["duration"].StringValue()
	reason := getOptString(opts, "reason")

	dur, err := util.ParseDuration(durationStr)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("Invalid duration: %s", err))
		return
	}

	banner := b.newBanner()
	resp, _, err := b.Moderation.Mute(banner, callerID, targetUser.ID, dur, reason, b.Config)
	if err != nil {
		b.Logger.Error("mute failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing mute.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleWarn(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have warn permissions.")
		return
	}
	targetUser := opts["user"].UserValue(s)
	reason := getOptString(opts, "reason")
	banner := b.newBanner()

	resp, extras, _, err := b.Warn.Warn(banner, callerID, targetUser.ID, reason, b.Config)
	if err != nil {
		b.Logger.Error("warn failed", zap.Error(err))
		respondEphemeral(s, i, "Error executing warn.")
		return
	}
	respondPublic(s, i, resp)
	for _, extra := range extras {
		s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{Content: suppressDiscordEmbeds(extra), Flags: discordgo.MessageFlagsSuppressEmbeds})
	}
}

func (b *Bot) handleWarnings(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption) {
	targetUser := opts["user"].UserValue(s)
	banner := b.newBanner()
	resp, err := b.Warn.Warnings(banner, targetUser.ID)
	if err != nil {
		b.Logger.Error("warnings error", zap.Error(err))
		respondEphemeral(s, i, "Error fetching warnings.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleUnwarn(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have unwarn permissions.")
		return
	}
	targetUser := opts["user"].UserValue(s)
	index := int(opts["id"].IntValue())
	banner := b.newBanner()

	resp, _, err := b.Warn.Unwarn("discord", callerID, targetUser.ID, index, banner)
	if err != nil {
		b.Logger.Error("unwarn error", zap.Error(err))
		respondEphemeral(s, i, fmt.Sprintf("Error: %s", err))
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleDehoist(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have dehoist permissions.")
		return
	}

	dry := getOptBool(opts, "dry")
	var targetID string
	if opt, ok := opts["user"]; ok {
		targetID = opt.UserValue(s).ID
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
		if err := editDeferredResponse(s, i, "Output too large - sent to your DMs."); err != nil {
			b.Logger.Error("failed to edit deferred dehoist response", zap.Error(err))
		}
		return
	}

	if err := editDeferredResponse(s, i, resp); err != nil {
		b.Logger.Error("failed to edit deferred dehoist response", zap.Error(err))
	}
}

func (b *Bot) handleAddAdmin(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	targetUser := opts["user"].UserValue(s)
	banner := b.newBanner()
	resp, err := b.Admin.AddAdmin(banner, callerID, targetUser.ID, b.Config)
	if err != nil {
		b.Logger.Error("addadmin error", zap.Error(err))
		respondEphemeral(s, i, "Error adding admin.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handleRemoveAdmin(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	targetUser := opts["user"].UserValue(s)
	banner := b.newBanner()
	resp, err := b.Admin.RemoveAdmin(banner, callerID, targetUser.ID, b.Config)
	if err != nil {
		b.Logger.Error("removeadmin error", zap.Error(err))
		respondEphemeral(s, i, "Error removing admin.")
		return
	}
	respondPublic(s, i, resp)
}

func (b *Bot) handlePing(s *discordgo.Session, i *discordgo.InteractionCreate) {
	text, err := b.Ping.Ping()
	if err != nil {
		b.Logger.Error("ping error", zap.Error(err))
		respondEphemeral(s, i, "Error checking ping.")
		return
	}
	respondPublic(s, i, text)
}

func (b *Bot) handlePurge(s *discordgo.Session, i *discordgo.InteractionCreate, opts map[string]*discordgo.ApplicationCommandInteractionDataOption, callerID string) {
	if !b.DB.IsAdmin("discord", callerID, b.Config) {
		respondEphemeral(s, i, "You don't have purge permissions.")
		return
	}

	// Check if this is a reply to another message
	var targetMessageID string
	if i.Interaction.Type == discordgo.InteractionApplicationCommand {
		// Try to get the message from the channel to check for reply
		msgs, err := s.ChannelMessages(i.ChannelID, 1, "", "", i.ID)
		if err == nil && len(msgs) > 0 {
			// Look for a referenced message
			if msgs[0].ReferencedMessage != nil {
				targetMessageID = msgs[0].ReferencedMessage.ID
			}
		}

		// Also check resolved messages from Discord
		interactionData := i.ApplicationCommandData()
		if interactionData.Resolved != nil && interactionData.Resolved.Messages != nil {
			// Try to find a referenced message
			for _, msg := range interactionData.Resolved.Messages {
				if msg != nil && msg.ID != i.ID {
					targetMessageID = msg.ID
					break
				}
			}
		}
	}

	// Alternative: check for count option
	var count int64 = 0
	if opt, ok := opts["count"]; ok {
		count = opt.IntValue()
	}

	// If we have a target message from reply, delete messages from current to target
	if targetMessageID != "" {
		// Defer the response since this might take time
		if err := deferResponse(s, i, true); err != nil {
			b.Logger.Error("failed to defer purge response", zap.Error(err))
			return
		}

		deleted, err := b.purgeMessagesBetween(s, i.ChannelID, i.ID, targetMessageID)
		if err != nil {
			b.Logger.Error("purge failed", zap.Error(err))
			editDeferredResponse(s, i, fmt.Sprintf("Error executing purge: %s", err))
			return
		}

		editDeferredResponse(s, i, fmt.Sprintf("🗑️ Deleted %d messages.", deleted))
	} else if count > 0 {
		// Delete last N messages
		if count > 100 {
			count = 100
		}

		// Defer the response
		if err := deferResponse(s, i, true); err != nil {
			b.Logger.Error("failed to defer purge response", zap.Error(err))
			return
		}

		msgs, err := s.ChannelMessages(i.ChannelID, int(count)+1, "", "", i.ID)
		if err != nil {
			b.Logger.Error("failed to get messages for purge", zap.Error(err))
			editDeferredResponse(s, i, "Error fetching messages.")
			return
		}

		var toDelete []string
		for _, msg := range msgs {
			if msg.ID != i.ID { // Don't delete the command message yet
				toDelete = append(toDelete, msg.ID)
			}
		}

		if len(toDelete) > 1 {
			s.ChannelMessagesBulkDelete(i.ChannelID, toDelete)
		} else if len(toDelete) == 1 {
			s.ChannelMessageDelete(i.ChannelID, toDelete[0])
		}

		// Delete the command message
		s.ChannelMessageDelete(i.ChannelID, i.ID)

		editDeferredResponse(s, i, fmt.Sprintf("🗑️ Deleted %d messages.", len(toDelete)))
	} else {
		respondEphemeral(s, i, "Please reply to a message or provide a count to purge.")
	}
}

// purgeMessagesBetween deletes all messages between two message IDs (inclusive of the target)
func (b *Bot) purgeMessagesBetween(s *discordgo.Session, channelID, commandID, targetID string) (int, error) {
	var allMessages []string
	var beforeID string

	// Keep fetching messages until we find the target
	for {
		msgs, err := s.ChannelMessages(channelID, 100, beforeID, "", commandID)
		if err != nil {
			return 0, err
		}

		if len(msgs) == 0 {
			break
		}

		for _, msg := range msgs {
			if msg.ID == targetID {
				// Found the target, include it and stop
				allMessages = append(allMessages, msg.ID)
				beforeID = ""
				break
			}
			allMessages = append(allMessages, msg.ID)
		}

		if beforeID == "" {
			break // Found target or reached end
		}

		beforeID = msgs[len(msgs)-1].ID
	}

	// Delete messages in chunks
	deleted := 0
	for len(allMessages) > 0 {
		chunkSize := 100
		if len(allMessages) < chunkSize {
			chunkSize = len(allMessages)
		}

		chunk := allMessages[:chunkSize]
		allMessages = allMessages[chunkSize:]

		if len(chunk) > 1 {
			s.ChannelMessagesBulkDelete(channelID, chunk)
		} else if len(chunk) == 1 {
			s.ChannelMessageDelete(channelID, chunk[0])
		}
		deleted += len(chunk)
	}

	// Delete the command message
	s.ChannelMessageDelete(channelID, commandID)

	return deleted, nil
}

func (b *Bot) handleRefreshStarboard(s *discordgo.Session, i *discordgo.InteractionCreate, callerID string) {
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

	editDeferredResponse(s, i, "✅ Starboard refreshed successfully.")
}

// --- Helpers ---

func optionMap(opts []*discordgo.ApplicationCommandInteractionDataOption) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	m := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(opts))
	for _, opt := range opts {
		m[opt.Name] = opt
	}
	return m
}

func (b *Bot) handleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID != b.Config.DiscordGuildID {
		return
	}

	data := i.ApplicationCommandData()
	if data.Name != "unwarn" {
		return
	}

	opts := optionMap(data.Options)
	userOpt, ok := opts["user"]
	if !ok {
		respondAutocomplete(s, i, nil)
		return
	}

	targetUser := userOpt.UserValue(s)
	if targetUser == nil {
		respondAutocomplete(s, i, nil)
		return
	}

	warnings, err := b.DB.GetWarnings("discord", targetUser.ID)
	if err != nil {
		b.Logger.Error("autocomplete warnings error", zap.Error(err))
		respondAutocomplete(s, i, nil)
		return
	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, min(len(warnings), 25))
	for idx, warning := range warnings {
		if len(choices) == 25 {
			break
		}

		reason := warning.Reason
		if reason == "" {
			reason = "no reason"
		}
		label := fmt.Sprintf("%d - %s", idx+1, truncateForChoice(reason, 90))
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  label,
			Value: idx + 1,
		})
	}

	respondAutocomplete(s, i, choices)
}

func respondAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate, choices []*discordgo.ApplicationCommandOptionChoice) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
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

func getOptString(opts map[string]*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	if opt, ok := opts[name]; ok {
		return opt.StringValue()
	}
	return ""
}

func getOptBool(opts map[string]*discordgo.ApplicationCommandInteractionDataOption, name string) bool {
	if opt, ok := opts[name]; ok {
		return opt.BoolValue()
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

// Discord event handlers for automatic dehoisting

func (b *Bot) onGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	if m.GuildID != b.Config.DiscordGuildID {
		return
	}

	// Run dehoisting for the new member after a short delay to ensure member data is available
	go func() {
		time.Sleep(2 * time.Second)

		// Check if we have permission to manage this user's nickname
		botMember, err := s.GuildMember(m.GuildID, s.State.User.ID)
		if err != nil {
			b.Logger.Debug("failed to get bot member for permission check on new member",
				zap.String("userID", m.User.ID), zap.Error(err))
			return
		}

		// Check if the bot's highest role is higher than the target's highest role
		canManage := canManageMember(s, m.GuildID, botMember, m.Member)
		if !canManage {
			b.Logger.Debug("skipping dehoist for new member - insufficient permissions",
				zap.String("userID", m.User.ID),
				zap.String("displayName", m.Nick))
			return
		}

		banner := b.newBanner()
		displayName, err := banner.GetDisplayName(m.User.ID)
		if err != nil {
			b.Logger.Error("failed to get display name for new member",
				zap.String("userID", m.User.ID), zap.Error(err))
			return
		}

		// Check if dehoisting is needed
		if needsDehoisting(displayName) {
			b.Logger.Info("auto-dehoisting new member",
				zap.String("userID", m.User.ID),
				zap.String("displayName", displayName))

			_, err := b.Moderation.Dehoist(banner, m.User.ID, false, b.Config)
			if err != nil {
				b.Logger.Error("failed to dehoist new member",
					zap.String("userID", m.User.ID), zap.Error(err))
			}
		}
	}()
}

func (b *Bot) onGuildMemberUpdate(s *discordgo.Session, m *discordgo.GuildMemberUpdate) {
	if m.GuildID != b.Config.DiscordGuildID {
		return
	}

	// Check if we have permission to manage this user's nickname
	botMember, err := s.GuildMember(m.GuildID, s.State.User.ID)
	if err != nil {
		b.Logger.Debug("failed to get bot member for permission check",
			zap.String("userID", m.User.ID), zap.Error(err))
		return
	}

	// Check if the bot's highest role is higher than the target's highest role
	canManage := canManageMember(s, m.GuildID, botMember, m.Member)
	if !canManage {
		b.Logger.Debug("skipping dehoist - insufficient permissions",
			zap.String("userID", m.User.ID),
			zap.String("displayName", m.Nick))
		return
	}

	// Check if the user changed their nickname (we only auto-dehoist Discord changes)
	// We can't detect if it was a Discord vs manual change, so we'll dehoist any hoisting characters
	banner := b.newBanner()
	displayName, err := banner.GetDisplayName(m.User.ID)
	if err != nil {
		b.Logger.Error("failed to get display name for updated member",
			zap.String("userID", m.User.ID), zap.Error(err))
		return
	}

	// Check if dehoisting is needed
	if needsDehoisting(displayName) {
		b.Logger.Info("auto-dehoisting updated member",
			zap.String("userID", m.User.ID),
			zap.String("displayName", displayName))

		_, err := b.Moderation.Dehoist(banner, m.User.ID, false, b.Config)
		if err != nil {
			b.Logger.Error("failed to dehoist updated member",
				zap.String("userID", m.User.ID), zap.Error(err))
		}
	}
}

// needsDehoisting checks if a display name starts with non-alphanumeric characters
func needsDehoisting(name string) bool {
	if len(name) == 0 {
		return false
	}

	// Get the first rune of the name
	for _, r := range name {
		isAlphanumeric := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		return !isAlphanumeric
	}
	return false
}

// canManageMember checks if the bot can manage a member's nickname
func canManageMember(s *discordgo.Session, guildID string, botMember, targetMember *discordgo.Member) bool {
	// Get the guild to check roles
	guild, err := s.Guild(guildID)
	if err != nil {
		return false
	}

	// Create a map of role positions
	rolePositions := make(map[string]int)
	for _, role := range guild.Roles {
		rolePositions[role.ID] = role.Position
	}

	// Find bot's highest role position
	botHighestPos := -1
	for _, roleID := range botMember.Roles {
		if pos, ok := rolePositions[roleID]; ok && pos > botHighestPos {
			botHighestPos = pos
		}
	}

	// Find target's highest role position
	targetHighestPos := -1
	for _, roleID := range targetMember.Roles {
		if pos, ok := rolePositions[roleID]; ok && pos > targetHighestPos {
			targetHighestPos = pos
		}
	}

	// Bot can manage if its highest role is higher than target's highest role
	return botHighestPos > targetHighestPos
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
