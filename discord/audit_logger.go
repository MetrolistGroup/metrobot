package discord

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const (
	serverLogChannelID   = "1417290212278538310"
	messageLogChannelID  = "1417289373124132864"
	reactionLogChannelID = "1547010470332989571"

	messageLogCachePerChannel = 1000
)

type cachedLogMessage struct {
	ID         string
	ChannelID  string
	GuildID    string
	AuthorID   string
	AuthorName string
	CreatedAt  time.Time
	Payload    string
	Bot        bool
	Webhook    bool
}

type permissionLabel struct {
	bit  int64
	name string
}

var permissionLabels = []permissionLabel{
	{discordgo.PermissionCreateInstantInvite, "Create Invite"},
	{discordgo.PermissionKickMembers, "Kick Members"},
	{discordgo.PermissionBanMembers, "Ban Members"},
	{discordgo.PermissionAdministrator, "Administrator"},
	{discordgo.PermissionManageChannels, "Manage Channels"},
	{discordgo.PermissionManageGuild, "Manage Server"},
	{discordgo.PermissionAddReactions, "Add Reactions"},
	{discordgo.PermissionViewAuditLogs, "View Audit Log"},
	{discordgo.PermissionVoicePrioritySpeaker, "Priority Speaker"},
	{discordgo.PermissionVoiceStreamVideo, "Video"},
	{discordgo.PermissionViewChannel, "View Channel"},
	{discordgo.PermissionSendMessages, "Send Messages"},
	{discordgo.PermissionSendTTSMessages, "Send TTS Messages"},
	{discordgo.PermissionManageMessages, "Manage Messages"},
	{discordgo.PermissionEmbedLinks, "Embed Links"},
	{discordgo.PermissionAttachFiles, "Attach Files"},
	{discordgo.PermissionReadMessageHistory, "Read Message History"},
	{discordgo.PermissionMentionEveryone, "Mention Everyone"},
	{discordgo.PermissionUseExternalEmojis, "Use External Emoji"},
	{discordgo.PermissionViewGuildInsights, "View Server Insights"},
	{discordgo.PermissionVoiceConnect, "Connect"},
	{discordgo.PermissionVoiceSpeak, "Speak"},
	{discordgo.PermissionVoiceMuteMembers, "Mute Members"},
	{discordgo.PermissionVoiceDeafenMembers, "Deafen Members"},
	{discordgo.PermissionVoiceMoveMembers, "Move Members"},
	{discordgo.PermissionVoiceUseVAD, "Use Voice Activity"},
	{discordgo.PermissionChangeNickname, "Change Nickname"},
	{discordgo.PermissionManageNicknames, "Manage Nicknames"},
	{discordgo.PermissionManageRoles, "Manage Roles"},
	{discordgo.PermissionManageWebhooks, "Manage Webhooks"},
	{discordgo.PermissionManageGuildExpressions, "Manage Expressions"},
	{discordgo.PermissionUseApplicationCommands, "Use Application Commands"},
	{discordgo.PermissionVoiceRequestToSpeak, "Request to Speak"},
	{discordgo.PermissionManageEvents, "Manage Events"},
	{discordgo.PermissionManageThreads, "Manage Threads"},
	{discordgo.PermissionCreatePublicThreads, "Create Public Threads"},
	{discordgo.PermissionCreatePrivateThreads, "Create Private Threads"},
	{discordgo.PermissionUseExternalStickers, "Use External Stickers"},
	{discordgo.PermissionSendMessagesInThreads, "Send Messages in Threads"},
	{discordgo.PermissionUseEmbeddedActivities, "Use Activities"},
	{discordgo.PermissionModerateMembers, "Timeout Members"},
	{discordgo.PermissionViewCreatorMonetizationAnalytics, "View Creator Analytics"},
	{discordgo.PermissionUseSoundboard, "Use Soundboard"},
	{discordgo.PermissionCreateGuildExpressions, "Create Expressions"},
	{discordgo.PermissionCreateEvents, "Create Events"},
	{discordgo.PermissionUseExternalSounds, "Use External Sounds"},
	{discordgo.PermissionSendVoiceMessages, "Send Voice Messages"},
	{discordgo.PermissionSendPolls, "Send Polls"},
	{discordgo.PermissionUseExternalApps, "Use External Apps"},
}

func (b *Bot) onGuildRoleCreateLog(s *discordgo.Session, event *discordgo.GuildRoleCreate) {
	if event.GuildID != b.Config.DiscordGuildID || event.Role == nil {
		return
	}
	entry := b.findAuditEntry(s, discordgo.AuditLogActionRoleCreate, event.Role.ID, nil)
	body := fmt.Sprintf("**Role:** %s (`%s`)\n**Permissions:** %s", event.Role.Mention(), event.Role.ID, permissionNames(event.Role.Permissions))
	b.sendEventLog(s, serverLogChannelID, "Role created", body+auditDetails(entry), "role-created.txt")
}

func (b *Bot) onGuildRoleUpdateLog(s *discordgo.Session, event *discordgo.GuildRoleUpdate) {
	if event.GuildID != b.Config.DiscordGuildID || event.Role == nil {
		return
	}
	entry := b.findAuditEntry(s, discordgo.AuditLogActionRoleUpdate, event.Role.ID, nil)
	body := fmt.Sprintf("**Role:** %s (`%s`)", event.Role.Mention(), event.Role.ID)
	if entry != nil {
		body += "\n" + formatAuditChanges(entry.Changes)
	} else {
		body += "\n**Current permissions:** " + permissionNames(event.Role.Permissions)
	}
	b.sendEventLog(s, serverLogChannelID, "Role updated", body+auditDetails(entry), "role-updated.txt")
}

func (b *Bot) onGuildRoleDeleteLog(s *discordgo.Session, event *discordgo.GuildRoleDelete) {
	if event.GuildID != b.Config.DiscordGuildID {
		return
	}
	entry := b.findAuditEntry(s, discordgo.AuditLogActionRoleDelete, event.RoleID, nil)
	body := fmt.Sprintf("**Role ID:** `%s`", event.RoleID)
	if entry != nil {
		body += "\n" + formatAuditChanges(entry.Changes)
	}
	b.sendEventLog(s, serverLogChannelID, "Role deleted", body+auditDetails(entry), "role-deleted.txt")
}

func (b *Bot) onChannelUpdateLog(s *discordgo.Session, event *discordgo.ChannelUpdate) {
	if event.GuildID != b.Config.DiscordGuildID || event.BeforeUpdate == nil {
		return
	}

	for _, change := range changedOverwrites(event.BeforeUpdate.PermissionOverwrites, event.PermissionOverwrites) {
		action := discordgo.AuditLogActionChannelOverwriteUpdate
		title := "Channel permission overwrite updated"
		if change.old == nil {
			action = discordgo.AuditLogActionChannelOverwriteCreate
			title = "Channel permission overwrite created"
		} else if change.current == nil {
			action = discordgo.AuditLogActionChannelOverwriteDelete
			title = "Channel permission overwrite deleted"
		}
		entry := b.findAuditEntry(s, action, event.ID, func(entry *discordgo.AuditLogEntry) bool {
			return entry.Options == nil || entry.Options.ID == change.id
		})
		body := fmt.Sprintf("**Channel:** <#%s> (`%s`)\n**Target:** %s\n%s", event.ID, event.ID, overwriteTarget(change), formatOverwriteChange(change))
		b.sendEventLog(s, serverLogChannelID, title, body+auditDetails(entry), "channel-permissions.txt")
	}
}

func (b *Bot) onGuildMemberUpdateLog(s *discordgo.Session, event *discordgo.GuildMemberUpdate) {
	if event.GuildID != b.Config.DiscordGuildID || event.User == nil || event.BeforeUpdate == nil {
		return
	}

	added, removed := changedIDs(event.BeforeUpdate.Roles, event.Roles)
	if len(added) > 0 || len(removed) > 0 {
		entry := b.findAuditEntry(s, discordgo.AuditLogActionMemberRoleUpdate, event.User.ID, nil)
		var lines []string
		if len(added) > 0 {
			lines = append(lines, "**Added:** "+roleLabels(s, event.GuildID, added))
		}
		if len(removed) > 0 {
			lines = append(lines, "**Removed:** "+roleLabels(s, event.GuildID, removed))
		}
		body := fmt.Sprintf("**Member:** <@%s> (`%s`)\n%s", event.User.ID, event.User.ID, strings.Join(lines, "\n"))
		b.sendEventLog(s, serverLogChannelID, "Member roles updated", body+auditDetails(entry), "member-roles.txt")
	}

	oldTimeout := timeoutValue(event.BeforeUpdate.CommunicationDisabledUntil)
	newTimeout := timeoutValue(event.CommunicationDisabledUntil)
	if sameTime(oldTimeout, newTimeout) {
		return
	}
	active := newTimeout != nil && newTimeout.After(time.Now())
	entry := b.findAuditEntry(s, discordgo.AuditLogActionMemberUpdate, event.User.ID, func(entry *discordgo.AuditLogEntry) bool {
		return timeoutAuditMatches(entry, active)
	})
	title := "Member timeout updated"
	if oldTimeout == nil && active {
		title = "Member timed out"
	} else if !active {
		title = "Member timeout removed"
	}
	body := fmt.Sprintf("**Member:** <@%s> (`%s`)", event.User.ID, event.User.ID)
	if oldTimeout != nil {
		body += "\n**Previous end:** " + discordTimestamp(*oldTimeout)
	}
	if newTimeout != nil {
		body += "\n**New end:** " + discordTimestamp(*newTimeout)
	}
	b.sendEventLog(s, serverLogChannelID, title, body+auditDetails(entry), "member-timeout.txt")
}

func (b *Bot) onGuildMemberRemoveLog(s *discordgo.Session, event *discordgo.GuildMemberRemove) {
	if event.GuildID != b.Config.DiscordGuildID || event.User == nil {
		return
	}
	entry := b.findAuditEntry(s, discordgo.AuditLogActionMemberKick, event.User.ID, nil)
	if entry == nil {
		return // Ordinary departures and bans are handled elsewhere.
	}
	body := fmt.Sprintf("**Member:** <@%s> (`%s`)%s", event.User.ID, event.User.ID, auditDetails(entry))
	b.sendEventLog(s, serverLogChannelID, "Member kicked", body, "member-kicked.txt")
}

func (b *Bot) onGuildBanAddLog(s *discordgo.Session, event *discordgo.GuildBanAdd) {
	if event.GuildID != b.Config.DiscordGuildID || event.User == nil {
		return
	}
	entry := b.findAuditEntry(s, discordgo.AuditLogActionMemberBanAdd, event.User.ID, nil)
	body := fmt.Sprintf("**Member:** <@%s> (`%s`)%s", event.User.ID, event.User.ID, auditDetails(entry))
	b.sendEventLog(s, serverLogChannelID, "Member banned", body, "member-banned.txt")
}

func (b *Bot) onGuildBanRemoveLog(s *discordgo.Session, event *discordgo.GuildBanRemove) {
	if event.GuildID != b.Config.DiscordGuildID || event.User == nil {
		return
	}
	entry := b.findAuditEntry(s, discordgo.AuditLogActionMemberBanRemove, event.User.ID, nil)
	body := fmt.Sprintf("**Member:** <@%s> (`%s`)%s", event.User.ID, event.User.ID, auditDetails(entry))
	b.sendEventLog(s, serverLogChannelID, "Member unbanned", body, "member-unbanned.txt")
}

func (b *Bot) onReactionAddLog(s *discordgo.Session, event *discordgo.MessageReactionAdd) {
	if event.MessageReaction == nil || b.eventLogExcluded(s, event.GuildID, event.ChannelID) {
		return
	}
	if event.Member != nil && event.Member.User != nil && event.Member.User.Bot || b.reactionUserIsBot(s, event.GuildID, event.UserID) {
		return
	}
	b.logReaction(s, "Reaction added", event.MessageReaction)
}

func (b *Bot) onReactionRemoveLog(s *discordgo.Session, event *discordgo.MessageReactionRemove) {
	if event.MessageReaction == nil || b.eventLogExcluded(s, event.GuildID, event.ChannelID) || b.reactionUserIsBot(s, event.GuildID, event.UserID) {
		return
	}
	b.logReaction(s, "Reaction removed", event.MessageReaction)
}

func (b *Bot) logReaction(s *discordgo.Session, title string, reaction *discordgo.MessageReaction) {
	emoji := reaction.Emoji.MessageFormat()
	if reaction.Emoji.ID != "" {
		emoji += fmt.Sprintf(" (`%s:%s`)", reaction.Emoji.Name, reaction.Emoji.ID)
	}
	body := fmt.Sprintf("**User:** <@%s> (`%s`)\n**Emoji:** %s\n**Channel:** <#%s>\n**Message:** %s", reaction.UserID, reaction.UserID, emoji, reaction.ChannelID, messageURL(reaction.GuildID, reaction.ChannelID, reaction.MessageID))
	b.sendEventLog(s, reactionLogChannelID, title, body, "reaction.txt")
}

func (b *Bot) onMessageUpdateLog(s *discordgo.Session, event *discordgo.MessageUpdate) {
	if event.Message == nil || b.eventLogExcluded(s, event.GuildID, event.ChannelID) {
		return
	}

	before, found := b.cachedMessage(event.ChannelID, event.ID, false)
	if event.BeforeUpdate != nil {
		before = cacheLogMessage(event.BeforeUpdate)
		found = true
	}
	current := event.Message
	if s.State != nil {
		if stateMessage, err := s.State.Message(event.ChannelID, event.ID); err == nil {
			current = stateMessage
			if event.EditedTimestamp != nil && event.Content == "" {
				copy := *stateMessage
				copy.Content = ""
				current = &copy
			}
		}
	}
	after := cacheLogMessage(current)
	if found {
		fillCachedIdentity(&after, before)
	}
	if after.Bot || after.Webhook {
		b.cachedMessage(event.ChannelID, event.ID, true)
		return
	}
	b.storeCachedMessage(after)
	if found && before.Payload == after.Payload {
		return
	}

	body := messageMetadata(after)
	if found {
		body += "\n\n**Before**\n" + before.Payload
	} else {
		body += "\n\n**Before**\n> *(content unavailable)*"
	}
	body += "\n\n**After**\n" + after.Payload
	b.sendEventLog(s, messageLogChannelID, "Message edited", body, "message-edited-"+event.ID+".txt")
}

func (b *Bot) onMessageDeleteLog(s *discordgo.Session, event *discordgo.MessageDelete) {
	if event.Message == nil || b.eventLogExcluded(s, event.GuildID, event.ChannelID) {
		return
	}

	message, found := b.cachedMessage(event.ChannelID, event.ID, true)
	if event.BeforeDelete != nil {
		message = cacheLogMessage(event.BeforeDelete)
		found = true
	}
	if found && (message.Bot || message.Webhook) {
		return
	}
	if !found {
		message = cacheLogMessage(event.Message)
	}
	body := messageMetadata(message) + "\n\n**Deleted content**\n"
	if found {
		body += message.Payload
	} else {
		body += "> *(content unavailable)*"
	}
	b.sendEventLog(s, messageLogChannelID, "Message deleted", body, "message-deleted-"+event.ID+".txt")
}

func (b *Bot) onMessageDeleteBulkLog(s *discordgo.Session, event *discordgo.MessageDeleteBulk) {
	if b.eventLogExcluded(s, event.GuildID, event.ChannelID) {
		return
	}
	var details strings.Builder
	included := 0
	for _, id := range event.Messages {
		message, found := b.cachedMessage(event.ChannelID, id, true)
		if found && (message.Bot || message.Webhook) {
			continue
		}
		included++
		details.WriteString("\n\n---\n\n")
		if found {
			details.WriteString(messageMetadata(message))
			details.WriteString("\n\n")
			details.WriteString(message.Payload)
		} else {
			fmt.Fprintf(&details, "**Message ID:** `%s`\n> *(content unavailable)*", id)
		}
	}
	if included == 0 {
		return
	}
	body := fmt.Sprintf("**Channel:** <#%s>\n**Messages:** %d%s", event.ChannelID, included, details.String())
	b.sendEventLog(s, messageLogChannelID, "Messages bulk deleted", body, "messages-bulk-deleted.txt")
}

func (b *Bot) rememberMessageForLogs(s *discordgo.Session, message *discordgo.Message) {
	if message == nil || b.eventLogExcluded(s, message.GuildID, message.ChannelID) {
		return
	}
	cached := cacheLogMessage(message)
	if cached.AuthorID == "" || cached.Bot || cached.Webhook {
		return
	}
	b.storeCachedMessage(cached)
}

func (b *Bot) storeCachedMessage(message cachedLogMessage) {
	if message.ID == "" || message.ChannelID == "" {
		return
	}
	b.messageLogMu.Lock()
	defer b.messageLogMu.Unlock()
	if b.messageLogCache == nil {
		b.messageLogCache = make(map[string][]cachedLogMessage)
	}
	messages := b.messageLogCache[message.ChannelID]
	for i := range messages {
		if messages[i].ID == message.ID {
			messages[i] = message
			b.messageLogCache[message.ChannelID] = messages
			return
		}
	}
	messages = append(messages, message)
	if len(messages) > messageLogCachePerChannel {
		// ponytail: bounded linear cache; index it only if these short scans become measurable.
		messages = messages[len(messages)-messageLogCachePerChannel:]
	}
	b.messageLogCache[message.ChannelID] = messages
}

func (b *Bot) cachedMessage(channelID, messageID string, remove bool) (cachedLogMessage, bool) {
	b.messageLogMu.Lock()
	defer b.messageLogMu.Unlock()
	messages := b.messageLogCache[channelID]
	for i := range messages {
		if messages[i].ID != messageID {
			continue
		}
		message := messages[i]
		if remove {
			messages = append(messages[:i], messages[i+1:]...)
			b.messageLogCache[channelID] = messages
		}
		return message, true
	}
	return cachedLogMessage{}, false
}

func cacheLogMessage(message *discordgo.Message) cachedLogMessage {
	cached := cachedLogMessage{
		ID:        message.ID,
		ChannelID: message.ChannelID,
		GuildID:   message.GuildID,
		CreatedAt: message.Timestamp,
		Payload:   renderMessagePayload(message),
		Webhook:   message.WebhookID != "",
	}
	if message.Author != nil {
		cached.AuthorID = message.Author.ID
		cached.AuthorName = message.Author.Username
		cached.Bot = message.Author.Bot
	}
	return cached
}

func fillCachedIdentity(message *cachedLogMessage, fallback cachedLogMessage) {
	if message.AuthorID == "" {
		message.AuthorID = fallback.AuthorID
		message.AuthorName = fallback.AuthorName
		message.Bot = fallback.Bot
		message.Webhook = fallback.Webhook
	}
	if message.GuildID == "" {
		message.GuildID = fallback.GuildID
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = fallback.CreatedAt
	}
}

func renderMessagePayload(message *discordgo.Message) string {
	var body strings.Builder
	body.WriteString("**Text:**\n")
	body.WriteString(quoteLogText(message.Content, "*(no text content)*"))
	if len(message.Attachments) > 0 {
		body.WriteString("\n**Attachments:**")
		for _, attachment := range message.Attachments {
			if attachment == nil {
				continue
			}
			fmt.Fprintf(&body, "\n> %s (%s, %d bytes): %s", oneLine(attachment.Filename), oneLine(attachment.ContentType), attachment.Size, attachment.URL)
		}
	}
	if len(message.StickerItems) > 0 {
		body.WriteString("\n**Stickers:**")
		for _, sticker := range message.StickerItems {
			if sticker != nil {
				fmt.Fprintf(&body, "\n> %s (`%s`)", oneLine(sticker.Name), sticker.ID)
			}
		}
	}
	appendJSONLogSection(&body, "Embeds", message.Embeds)
	appendJSONLogSection(&body, "Components", message.Components)
	if message.Poll != nil {
		appendJSONLogSection(&body, "Poll", message.Poll)
	}
	return body.String()
}

func appendJSONLogSection(body *strings.Builder, title string, value interface{}) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil || string(data) == "[]" || string(data) == "null" {
		return
	}
	fmt.Fprintf(body, "\n**%s:**\n%s", title, quoteLogText(string(data), "*(none)*"))
}

func messageMetadata(message cachedLogMessage) string {
	author := "*(unknown)*"
	if message.AuthorID != "" {
		author = fmt.Sprintf("<@%s> (`%s`, %s)", message.AuthorID, message.AuthorID, oneLine(message.AuthorName))
	}
	body := fmt.Sprintf("**Author:** %s\n**Channel:** <#%s>\n**Message ID:** `%s`", author, message.ChannelID, message.ID)
	if !message.CreatedAt.IsZero() {
		body += "\n**Created:** " + discordTimestamp(message.CreatedAt)
	}
	return body
}

func (b *Bot) eventLogExcluded(s *discordgo.Session, guildID, channelID string) bool {
	if b.Config == nil || guildID != b.Config.DiscordGuildID || isEventLogChannel(channelID) {
		return true
	}
	if s != nil && s.State != nil {
		if channel, err := s.State.Channel(channelID); err == nil && channel.IsThread() && isEventLogChannel(channel.ParentID) {
			return true
		}
	}
	return false
}

func isEventLogChannel(channelID string) bool {
	return channelID == serverLogChannelID || channelID == messageLogChannelID || channelID == reactionLogChannelID
}

func (b *Bot) reactionUserIsBot(s *discordgo.Session, guildID, userID string) bool {
	if isSessionUser(s, userID) {
		return true
	}
	if s == nil || s.State == nil {
		return false
	}
	member, err := s.State.Member(guildID, userID)
	return err == nil && member.User != nil && member.User.Bot
}

func (b *Bot) findAuditEntry(s *discordgo.Session, action discordgo.AuditLogAction, targetID string, match func(*discordgo.AuditLogEntry) bool) *discordgo.AuditLogEntry {
	var lastErr error
	for _, delay := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second} {
		time.Sleep(delay)
		log, err := s.GuildAuditLog(b.Config.DiscordGuildID, "", "", int(action), 6)
		if err != nil {
			lastErr = err
			continue
		}
		for _, entry := range log.AuditLogEntries {
			if entry == nil || entry.ActionType == nil || *entry.ActionType != action || entry.TargetID != targetID || !freshAuditEntry(entry) {
				continue
			}
			if match == nil || match(entry) {
				return entry
			}
		}
	}
	if lastErr != nil && b.Logger != nil {
		b.Logger.Debug("failed to read Discord audit log", zap.Int("action", int(action)), zap.Error(lastErr))
	}
	return nil
}

func freshAuditEntry(entry *discordgo.AuditLogEntry) bool {
	created, err := discordgo.SnowflakeTimestamp(entry.ID)
	if err != nil {
		return false
	}
	age := time.Since(created)
	return age >= -5*time.Second && age <= 30*time.Second
}

func timeoutAuditMatches(entry *discordgo.AuditLogEntry, active bool) bool {
	for _, change := range entry.Changes {
		if change != nil && change.Key != nil && *change.Key == discordgo.AuditLogChangeKeyCommunicationDisabledUntil {
			return (change.NewValue != nil) == active
		}
	}
	return false
}

func auditDetails(entry *discordgo.AuditLogEntry) string {
	if entry == nil {
		return "\n**Actor:** *(unavailable)*"
	}
	body := fmt.Sprintf("\n**Actor:** <@%s> (`%s`)", entry.UserID, entry.UserID)
	if entry.Reason != "" {
		body += "\n**Reason:** " + quoteInline(entry.Reason)
	}
	return body
}

func formatAuditChanges(changes []*discordgo.AuditLogChange) string {
	var lines []string
	for _, change := range changes {
		if change == nil || change.Key == nil {
			continue
		}
		if *change.Key == discordgo.AuditLogChangeKeyPermissions {
			oldPermissions, oldOK := permissionValue(change.OldValue)
			newPermissions, newOK := permissionValue(change.NewValue)
			if oldOK && newOK {
				if added := newPermissions &^ oldPermissions; added != 0 {
					lines = append(lines, "**Permissions added:** "+permissionNames(added))
				}
				if removed := oldPermissions &^ newPermissions; removed != 0 {
					lines = append(lines, "**Permissions removed:** "+permissionNames(removed))
				}
				continue
			}
		}
		label := strings.ReplaceAll(string(*change.Key), "_", " ")
		lines = append(lines, fmt.Sprintf("**%s:** `%s` -> `%s`", capitalize(label), quoteInline(auditValue(change.OldValue)), quoteInline(auditValue(change.NewValue))))
	}
	if len(lines) == 0 {
		return "**Changes:** *(unavailable)*"
	}
	return strings.Join(lines, "\n")
}

func permissionValue(value interface{}) (int64, bool) {
	switch value := value.(type) {
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		return parsed, err == nil
	case float64:
		return int64(value), true
	case int64:
		return value, true
	case int:
		return int64(value), true
	default:
		return 0, false
	}
}

func permissionNames(permissions int64) string {
	if permissions == 0 {
		return "*(none)*"
	}
	var names []string
	var known int64
	for _, permission := range permissionLabels {
		known |= permission.bit
		if permissions&permission.bit != 0 {
			names = append(names, permission.name)
		}
	}
	if unknown := permissions &^ known; unknown != 0 {
		names = append(names, fmt.Sprintf("Unknown (`0x%x`)", unknown))
	}
	return strings.Join(names, ", ")
}

func changedIDs(oldIDs, newIDs []string) (added, removed []string) {
	oldSet := make(map[string]bool, len(oldIDs))
	newSet := make(map[string]bool, len(newIDs))
	for _, id := range oldIDs {
		oldSet[id] = true
	}
	for _, id := range newIDs {
		newSet[id] = true
		if !oldSet[id] {
			added = append(added, id)
		}
	}
	for _, id := range oldIDs {
		if !newSet[id] {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func roleLabels(s *discordgo.Session, guildID string, roleIDs []string) string {
	labels := make([]string, 0, len(roleIDs))
	for _, id := range roleIDs {
		label := fmt.Sprintf("<@&%s> (`%s`)", id, id)
		if s != nil && s.State != nil {
			if role, err := s.State.Role(guildID, id); err == nil {
				label = fmt.Sprintf("%s (`%s`, %s)", role.Mention(), id, oneLine(role.Name))
			}
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}

type overwriteChange struct {
	id      string
	old     *discordgo.PermissionOverwrite
	current *discordgo.PermissionOverwrite
}

func changedOverwrites(oldOverwrites, newOverwrites []*discordgo.PermissionOverwrite) []overwriteChange {
	oldByID := make(map[string]*discordgo.PermissionOverwrite, len(oldOverwrites))
	newByID := make(map[string]*discordgo.PermissionOverwrite, len(newOverwrites))
	ids := make(map[string]bool, len(oldOverwrites)+len(newOverwrites))
	for _, overwrite := range oldOverwrites {
		if overwrite != nil {
			oldByID[overwrite.ID] = overwrite
			ids[overwrite.ID] = true
		}
	}
	for _, overwrite := range newOverwrites {
		if overwrite != nil {
			newByID[overwrite.ID] = overwrite
			ids[overwrite.ID] = true
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	var changes []overwriteChange
	for _, id := range ordered {
		old, current := oldByID[id], newByID[id]
		if old == nil || current == nil || old.Allow != current.Allow || old.Deny != current.Deny || old.Type != current.Type {
			changes = append(changes, overwriteChange{id: id, old: old, current: current})
		}
	}
	return changes
}

func overwriteTarget(change overwriteChange) string {
	overwrite := change.current
	if overwrite == nil {
		overwrite = change.old
	}
	if overwrite != nil && overwrite.Type == discordgo.PermissionOverwriteTypeMember {
		return fmt.Sprintf("<@%s> (`%s`)", change.id, change.id)
	}
	return fmt.Sprintf("<@&%s> (`%s`)", change.id, change.id)
}

func formatOverwriteChange(change overwriteChange) string {
	if change.old == nil {
		return "**Allowed:** " + permissionNames(change.current.Allow) + "\n**Denied:** " + permissionNames(change.current.Deny)
	}
	if change.current == nil {
		return "**Previous allowed:** " + permissionNames(change.old.Allow) + "\n**Previous denied:** " + permissionNames(change.old.Deny)
	}
	var lines []string
	appendPermissionDelta := func(label string, old, current int64) {
		if added := current &^ old; added != 0 {
			lines = append(lines, fmt.Sprintf("**%s added:** %s", label, permissionNames(added)))
		}
		if removed := old &^ current; removed != 0 {
			lines = append(lines, fmt.Sprintf("**%s removed:** %s", label, permissionNames(removed)))
		}
	}
	appendPermissionDelta("Allowed", change.old.Allow, change.current.Allow)
	appendPermissionDelta("Denied", change.old.Deny, change.current.Deny)
	return strings.Join(lines, "\n")
}

func timeoutValue(timeout *time.Time) *time.Time {
	if timeout == nil || timeout.IsZero() {
		return nil
	}
	return timeout
}

func sameTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func discordTimestamp(timestamp time.Time) string {
	return fmt.Sprintf("<t:%d:F> (<t:%d:R>)", timestamp.Unix(), timestamp.Unix())
}

func messageURL(guildID, channelID, messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}

func auditValue(value interface{}) string {
	if value == nil {
		return "none"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func quoteInline(value string) string {
	return strings.ReplaceAll(oneLine(value), "`", "'")
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func quoteLogText(value, empty string) string {
	if value == "" {
		value = empty
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return "> " + strings.ReplaceAll(value, "\n", "\n> ")
}

func (b *Bot) sendEventLog(s *discordgo.Session, channelID, title, body, filename string) {
	content := "**" + title + "**\n" + body
	message := &discordgo.MessageSend{
		Content:         content,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Flags:           discordgo.MessageFlagsSuppressEmbeds,
	}
	if utf8.RuneCountInString(content) > 1950 {
		message.Content = "**" + title + "**\nFull details attached."
		message.Files = []*discordgo.File{{
			Name:        filename,
			ContentType: "text/plain; charset=utf-8",
			Reader:      strings.NewReader(title + "\n\n" + body),
		}}
	}
	if _, err := s.ChannelMessageSendComplex(channelID, message); err != nil && b.Logger != nil {
		b.Logger.Error("failed to send Discord event log", zap.String("channelID", channelID), zap.String("event", title), zap.Error(err))
	}
}
