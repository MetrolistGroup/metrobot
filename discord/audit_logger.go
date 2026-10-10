package discord

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/json/v2"
	"github.com/disgoorg/snowflake/v2"
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
	Message    *discord.Message
}

type permissionLabel struct {
	bit  discord.Permissions
	name string
}

var permissionLabels = []permissionLabel{
	{discord.PermissionCreateInstantInvite, "Create Invite"},
	{discord.PermissionKickMembers, "Kick Members"},
	{discord.PermissionBanMembers, "Ban Members"},
	{discord.PermissionAdministrator, "Administrator"},
	{discord.PermissionManageChannels, "Manage Channels"},
	{discord.PermissionManageGuild, "Manage Server"},
	{discord.PermissionAddReactions, "Add Reactions"},
	{discord.PermissionViewAuditLog, "View Audit Log"},
	{discord.PermissionPrioritySpeaker, "Priority Speaker"},
	{discord.PermissionStream, "Video"},
	{discord.PermissionViewChannel, "View Channel"},
	{discord.PermissionSendMessages, "Send Messages"},
	{discord.PermissionSendTTSMessages, "Send TTS Messages"},
	{discord.PermissionManageMessages, "Manage Messages"},
	{discord.PermissionEmbedLinks, "Embed Links"},
	{discord.PermissionAttachFiles, "Attach Files"},
	{discord.PermissionReadMessageHistory, "Read Message History"},
	{discord.PermissionMentionEveryone, "Mention Everyone"},
	{discord.PermissionUseExternalEmojis, "Use External Emoji"},
	{discord.PermissionViewGuildInsights, "View Server Insights"},
	{discord.PermissionConnect, "Connect"},
	{discord.PermissionSpeak, "Speak"},
	{discord.PermissionMuteMembers, "Mute Members"},
	{discord.PermissionDeafenMembers, "Deafen Members"},
	{discord.PermissionMoveMembers, "Move Members"},
	{discord.PermissionUseVAD, "Use Voice Activity"},
	{discord.PermissionChangeNickname, "Change Nickname"},
	{discord.PermissionManageNicknames, "Manage Nicknames"},
	{discord.PermissionManageRoles, "Manage Roles"},
	{discord.PermissionManageWebhooks, "Manage Webhooks"},
	{discord.PermissionManageGuildExpressions, "Manage Expressions"},
	{discord.PermissionUseApplicationCommands, "Use Application Commands"},
	{discord.PermissionRequestToSpeak, "Request to Speak"},
	{discord.PermissionManageEvents, "Manage Events"},
	{discord.PermissionManageThreads, "Manage Threads"},
	{discord.PermissionCreatePublicThreads, "Create Public Threads"},
	{discord.PermissionCreatePrivateThreads, "Create Private Threads"},
	{discord.PermissionUseExternalStickers, "Use External Stickers"},
	{discord.PermissionSendMessagesInThreads, "Send Messages in Threads"},
	{discord.PermissionUseEmbeddedActivities, "Use Activities"},
	{discord.PermissionModerateMembers, "Timeout Members"},
	{discord.PermissionViewCreatorMonetizationAnalytics, "View Creator Analytics"},
	{discord.PermissionUseSoundboard, "Use Soundboard"},
	{discord.PermissionCreateGuildExpressions, "Create Expressions"},
	{discord.PermissionCreateEvents, "Create Events"},
	{discord.PermissionUseExternalSounds, "Use External Sounds"},
	{discord.PermissionSendVoiceMessages, "Send Voice Messages"},
	{discord.PermissionSendPolls, "Send Polls"},
	{discord.PermissionUseExternalApps, "Use External Apps"},
}

func (b *Bot) onGuildRoleCreateLog(s *bot.Client, event *events.RoleCreate) {
	if event.GuildID.String() != b.Config.DiscordGuildID || event.Role.ID == 0 {
		return
	}
	entry := b.findAuditEntry(s, discord.AuditLogEventRoleCreate, event.Role.ID.String(), nil)
	body := fmt.Sprintf("**Role:** %s (`%s`)\n**Permissions:** %s", event.Role.Mention(), event.Role.ID, permissionNames(event.Role.Permissions))
	b.sendEventLog(s, serverLogChannelID, "Role created", body+auditDetails(entry), "role-created.txt")
}

func (b *Bot) onGuildRoleUpdateLog(s *bot.Client, event *events.RoleUpdate) {
	if event.GuildID.String() != b.Config.DiscordGuildID || event.Role.ID == 0 {
		return
	}
	entry := b.findAuditEntry(s, discord.AuditLogEventRoleUpdate, event.Role.ID.String(), nil)
	body := fmt.Sprintf("**Role:** %s (`%s`)", event.Role.Mention(), event.Role.ID)
	if entry != nil {
		body += "\n" + formatAuditChanges(entry.Changes)
	} else {
		body += "\n**Current permissions:** " + permissionNames(event.Role.Permissions)
	}
	b.sendEventLog(s, serverLogChannelID, "Role updated", body+auditDetails(entry), "role-updated.txt")
}

func (b *Bot) onGuildRoleDeleteLog(s *bot.Client, event *events.RoleDelete) {
	if event.GuildID.String() != b.Config.DiscordGuildID {
		return
	}
	entry := b.findAuditEntry(s, discord.AuditLogEventRoleDelete, event.RoleID.String(), nil)
	body := fmt.Sprintf("**Role ID:** `%s`", event.RoleID)
	if entry != nil {
		body += "\n" + formatAuditChanges(entry.Changes)
	}
	b.sendEventLog(s, serverLogChannelID, "Role deleted", body+auditDetails(entry), "role-deleted.txt")
}

func (b *Bot) onChannelUpdateLog(s *bot.Client, event *events.GuildChannelUpdate) {
	if event.GuildID.String() != b.Config.DiscordGuildID || event.OldChannel == nil {
		return
	}

	for _, change := range changedOverwrites(event.OldChannel.PermissionOverwrites(), event.Channel.PermissionOverwrites()) {
		action := discord.AuditLogEventChannelOverwriteUpdate
		title := "Channel permission overwrite updated"
		if change.old == nil {
			action = discord.AuditLogEventChannelOverwriteCreate
			title = "Channel permission overwrite created"
		} else if change.current == nil {
			action = discord.AuditLogEventChannelOverwriteDelete
			title = "Channel permission overwrite deleted"
		}
		entry := b.findAuditEntry(s, action, event.ChannelID.String(), func(entry *discord.AuditLogEntry) bool {
			return entry.Options == nil || value(entry.Options.ID) == change.id
		})
		body := fmt.Sprintf("**Channel:** <#%s> (`%s`)\n**Target:** %s\n%s", event.ChannelID, event.ChannelID, overwriteTarget(change), formatOverwriteChange(change))
		b.sendEventLog(s, serverLogChannelID, title, body+auditDetails(entry), "channel-permissions.txt")
	}
}

func (b *Bot) onGuildMemberUpdateLog(s *bot.Client, event *events.GuildMemberUpdate) {
	if event.GuildID.String() != b.Config.DiscordGuildID || event.Member.User.ID == 0 || event.OldMember.User.ID == 0 {
		return
	}

	added, removed := changedIDs(event.OldMember.RoleIDs, event.Member.RoleIDs)
	if len(added) > 0 || len(removed) > 0 {
		entry := b.findAuditEntry(s, discord.AuditLogEventMemberRoleUpdate, event.Member.User.ID.String(), nil)
		var lines []string
		if len(added) > 0 {
			lines = append(lines, "**Added:** "+roleLabels(s, event.GuildID.String(), added))
		}
		if len(removed) > 0 {
			lines = append(lines, "**Removed:** "+roleLabels(s, event.GuildID.String(), removed))
		}
		body := fmt.Sprintf("**Member:** <@%s> (`%s`)\n%s", event.Member.User.ID.String(), event.Member.User.ID.String(), strings.Join(lines, "\n"))
		b.sendEventLog(s, serverLogChannelID, "Member roles updated", body+auditDetails(entry), "member-roles.txt")
	}

	oldTimeout := timeoutValue(event.OldMember.CommunicationDisabledUntil)
	newTimeout := timeoutValue(event.Member.CommunicationDisabledUntil)
	if sameTime(oldTimeout, newTimeout) {
		return
	}
	active := newTimeout != nil && newTimeout.After(time.Now())
	entry := b.findAuditEntry(s, discord.AuditLogEventMemberUpdate, event.Member.User.ID.String(), func(entry *discord.AuditLogEntry) bool {
		return timeoutAuditMatches(entry, active)
	})
	title := "Member timeout updated"
	if oldTimeout == nil && active {
		title = "Member timed out"
	} else if !active {
		title = "Member timeout removed"
	}
	body := fmt.Sprintf("**Member:** <@%s> (`%s`)", event.Member.User.ID.String(), event.Member.User.ID.String())
	if oldTimeout != nil {
		body += "\n**Previous end:** " + discordTimestamp(*oldTimeout)
	}
	if newTimeout != nil {
		body += "\n**New end:** " + discordTimestamp(*newTimeout)
	}
	b.sendEventLog(s, serverLogChannelID, title, body+auditDetails(entry), "member-timeout.txt")
}

func (b *Bot) onGuildMemberRemoveLog(s *bot.Client, event *events.GuildMemberLeave) {
	if event.GuildID.String() != b.Config.DiscordGuildID || event.User.ID == 0 {
		return
	}
	entry := b.findAuditEntry(s, discord.AuditLogEventMemberKick, event.User.ID.String(), nil)
	if entry == nil {
		return // Ordinary departures and bans are handled elsewhere.
	}
	body := fmt.Sprintf("**Member:** <@%s> (`%s`)%s", event.User.ID.String(), event.User.ID.String(), auditDetails(entry))
	b.sendEventLog(s, serverLogChannelID, "Member kicked", body, "member-kicked.txt")
}

func (b *Bot) onGuildBanAddLog(s *bot.Client, event *events.GuildBan) {
	if event.GuildID.String() != b.Config.DiscordGuildID || event.User.ID == 0 {
		return
	}
	entry := b.findAuditEntry(s, discord.AuditLogEventMemberBanAdd, event.User.ID.String(), nil)
	body := fmt.Sprintf("**Member:** <@%s> (`%s`)%s", event.User.ID.String(), event.User.ID.String(), auditDetails(entry))
	b.sendEventLog(s, serverLogChannelID, "Member banned", body, "member-banned.txt")
}

func (b *Bot) onGuildBanRemoveLog(s *bot.Client, event *events.GuildUnban) {
	if event.GuildID.String() != b.Config.DiscordGuildID || event.User.ID == 0 {
		return
	}
	entry := b.findAuditEntry(s, discord.AuditLogEventMemberBanRemove, event.User.ID.String(), nil)
	body := fmt.Sprintf("**Member:** <@%s> (`%s`)%s", event.User.ID.String(), event.User.ID.String(), auditDetails(entry))
	b.sendEventLog(s, serverLogChannelID, "Member unbanned", body, "member-unbanned.txt")
}

func (b *Bot) onReactionAddLog(s *bot.Client, event *events.MessageReactionAdd) {
	if event.GenericReaction == nil || b.eventLogExcluded(s, snowflakeString(event.GuildID), event.ChannelID.String()) {
		return
	}
	if event.Member != nil && event.Member.User.Bot || b.reactionUserIsBot(s, snowflakeString(event.GuildID), event.UserID.String()) {
		return
	}
	b.logReaction(s, "Reaction added", event.GenericReaction)
}

func (b *Bot) onReactionRemoveLog(s *bot.Client, event *events.MessageReactionRemove) {
	if event.GenericReaction == nil || b.eventLogExcluded(s, snowflakeString(event.GuildID), event.ChannelID.String()) || b.reactionUserIsBot(s, snowflakeString(event.GuildID), event.UserID.String()) {
		return
	}
	b.logReaction(s, "Reaction removed", event.GenericReaction)
}

func (b *Bot) logReaction(s *bot.Client, title string, reaction *events.GenericReaction) {
	emoji := value(reaction.Emoji.Name)
	if reaction.Emoji.ID != nil {
		custom := discord.Emoji{ID: *reaction.Emoji.ID, Name: emoji, Animated: reaction.Emoji.Animated}
		emoji = custom.Mention() + fmt.Sprintf(" (`%s:%s`)", emoji, reaction.Emoji.ID)
	}
	body := fmt.Sprintf("**User:** <@%s> (`%s`)\n**Emoji:** %s\n**Channel:** <#%s>\n**Message:** %s", reaction.UserID, reaction.UserID, emoji, reaction.ChannelID, messageURL(snowflakeString(reaction.GuildID), reaction.ChannelID.String(), reaction.MessageID.String()))
	b.sendEventLog(s, reactionLogChannelID, title, body, "reaction.txt")
}

func (b *Bot) onMessageUpdateLog(s *bot.Client, event *events.MessageUpdate) {
	if b.eventLogExcluded(s, snowflakeString(event.GuildID), event.ChannelID.String()) {
		return
	}
	before, found := b.cachedMessage(event.ChannelID.String(), event.MessageID.String(), false)
	if event.OldMessage.ID != 0 {
		before = cacheLogMessage(&event.OldMessage)
		found = true
	}
	after := cacheLogMessage(&event.Message)
	if found {
		fillCachedIdentity(&after, before)
	}
	if after.Bot || after.Webhook {
		after.Payload, after.Message = "", nil
		b.storeCachedMessage(after)
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
	go b.sendEventLog(s, messageLogChannelID, "Message edited", body, "message-edited-"+event.MessageID.String()+".txt")
}

func (b *Bot) onMessageDeleteLog(s *bot.Client, event *events.MessageDelete) {
	if b.eventLogExcluded(s, snowflakeString(event.GuildID), event.ChannelID.String()) {
		return
	}
	message, found := b.cachedMessage(event.ChannelID.String(), event.MessageID.String(), true)
	if event.Message.ID != 0 {
		message = cacheLogMessage(&event.Message)
		found = true
	}
	if found && (message.Bot || message.Webhook) {
		return
	}
	if !found {
		message = cachedLogMessage{ID: event.MessageID.String(), ChannelID: event.ChannelID.String(), GuildID: snowflakeString(event.GuildID)}
	}
	body := messageMetadata(message) + "\n\n**Deleted content**\n"
	if found {
		body += message.Payload
	} else {
		body += "> *(content unavailable)*"
	}
	go b.sendEventLog(s, messageLogChannelID, "Message deleted", body, "message-deleted-"+event.MessageID.String()+".txt")
}

func (b *Bot) onMessageDeleteBulkLog(s *bot.Client, event *gateway.EventMessageDeleteBulk) {
	if b.eventLogExcluded(s, snowflakeString(event.GuildID), event.ChannelID.String()) {
		return
	}
	var details strings.Builder
	included := 0
	for _, id := range event.IDs {
		message, found := b.cachedMessage(event.ChannelID.String(), id.String(), true)
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
	go b.sendEventLog(s, messageLogChannelID, "Messages bulk deleted", body, "messages-bulk-deleted.txt")
}

func (b *Bot) rememberMessageForLogs(s *bot.Client, message *discord.Message) {
	if message == nil || b.eventLogExcluded(s, snowflakeString(message.GuildID), message.ChannelID.String()) {
		return
	}
	cached := cacheLogMessage(message)
	if cached.AuthorID == "" {
		return
	}
	if cached.Bot || cached.Webhook {
		// Keep only identity so later partial updates/deletes can still be filtered.
		cached.Payload, cached.Message = "", nil
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

func cacheLogMessage(message *discord.Message) cachedLogMessage {
	cached := cachedLogMessage{
		ID:        message.ID.String(),
		ChannelID: message.ChannelID.String(),
		GuildID:   snowflakeString(message.GuildID),
		CreatedAt: message.CreatedAt,
		Payload:   renderMessagePayload(message),
		Message:   message,
		Webhook:   message.WebhookID != nil,
	}
	if message.Author.ID != 0 {
		cached.AuthorID = message.Author.ID.String()
		cached.AuthorName = message.Author.Username
		cached.Bot = message.Author.Bot
	}
	return cached
}

func fillCachedIdentity(message *cachedLogMessage, fallback cachedLogMessage) {
	if message.AuthorID == "" {
		message.AuthorID = fallback.AuthorID
		message.AuthorName = fallback.AuthorName
	}
	message.Bot = message.Bot || fallback.Bot
	message.Webhook = message.Webhook || fallback.Webhook
	if message.GuildID == "" {
		message.GuildID = fallback.GuildID
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = fallback.CreatedAt
	}
}

func renderMessagePayload(message *discord.Message) string {
	var body strings.Builder
	body.WriteString("**Text:**\n")
	body.WriteString(quoteLogText(message.Content, "*(no text content)*"))
	if len(message.Attachments) > 0 {
		body.WriteString("\n**Attachments:**")
		for _, attachment := range message.Attachments {
			fmt.Fprintf(&body, "\n> %s (%s, %d bytes): %s", oneLine(attachment.Filename), oneLine(value(attachment.ContentType)), attachment.Size, attachment.URL)
		}
	}
	if len(message.StickerItems) > 0 {
		body.WriteString("\n**Stickers:**")
		for _, sticker := range message.StickerItems {
			if sticker.ID != 0 {
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

func (b *Bot) eventLogExcluded(s *bot.Client, guildID, channelID string) bool {
	if b.Config == nil || guildID != b.Config.DiscordGuildID || isEventLogChannel(channelID) {
		return true
	}
	if s != nil && s.Caches != nil {
		if channel, ok := s.Caches.Channel(snowflakeID(channelID)); ok && isThread(channel) && isEventLogChannel(snowflakeString(channel.ParentID())) {
			return true
		}
	}
	return false
}

func isEventLogChannel(channelID string) bool {
	return channelID == serverLogChannelID || channelID == messageLogChannelID || channelID == reactionLogChannelID
}

func (b *Bot) reactionUserIsBot(s *bot.Client, guildID, userID string) bool {
	if isSessionUser(s, userID) {
		return true
	}
	if s == nil || s.Caches == nil {
		return false
	}
	member, ok := s.Caches.Member(snowflakeID(guildID), snowflakeID(userID))
	return ok && member.User.Bot
}

func (b *Bot) findAuditEntry(s *bot.Client, action discord.AuditLogEvent, targetID string, match func(*discord.AuditLogEntry) bool) *discord.AuditLogEntry {
	var lastErr error
	for _, delay := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second} {
		time.Sleep(delay)
		log, err := s.Rest.GetAuditLog(snowflakeID(b.Config.DiscordGuildID), 0, action, 0, 0, 6)
		if err != nil {
			lastErr = err
			continue
		}
		for _, entry := range log.AuditLogEntries {
			if entry.ActionType != action || snowflakeString(entry.TargetID) != targetID || !freshAuditEntry(&entry) {
				continue
			}
			if match == nil || match(&entry) {
				return &entry
			}
		}
	}
	if lastErr != nil && b.Logger != nil {
		b.Logger.Debug("failed to read Discord audit log", zap.Int("action", int(action)), zap.Error(lastErr))
	}
	return nil
}

func freshAuditEntry(entry *discord.AuditLogEntry) bool {
	if entry.ID == 0 {
		return false
	}
	age := time.Since(entry.ID.Time())
	return age >= -5*time.Second && age <= 30*time.Second
}

func timeoutAuditMatches(entry *discord.AuditLogEntry, active bool) bool {
	for _, change := range entry.Changes {
		if change.Key == discord.AuditLogChangeKeyCommunicationDisabledUntil {
			return (len(change.NewValue) > 0 && string(change.NewValue) != "null") == active
		}
	}
	return false
}

func auditDetails(entry *discord.AuditLogEntry) string {
	if entry == nil {
		return "\n**Actor:** *(unavailable)*"
	}
	body := fmt.Sprintf("\n**Actor:** <@%s> (`%s`)", entry.UserID, entry.UserID)
	if value(entry.Reason) != "" {
		body += "\n**Reason:** " + quoteInline(value(entry.Reason))
	}
	return body
}

func formatAuditChanges(changes []discord.AuditLogChange) string {
	var lines []string
	for _, change := range changes {
		if change.Key == discord.AuditLogChangeKeyPermissions {
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
		label := strings.ReplaceAll(string(change.Key), "_", " ")
		lines = append(lines, fmt.Sprintf("**%s:** `%s` -> `%s`", capitalize(label), quoteInline(auditValue(change.OldValue)), quoteInline(auditValue(change.NewValue))))
	}
	if len(lines) == 0 {
		return "**Changes:** *(unavailable)*"
	}
	return strings.Join(lines, "\n")
}

func permissionValue(value interface{}) (discord.Permissions, bool) {
	switch value := value.(type) {
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(value, &decoded) != nil {
			return 0, false
		}
		return permissionValue(decoded)
	case discord.Permissions:
		return value, true
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		return discord.Permissions(parsed), err == nil
	case float64:
		return discord.Permissions(value), true
	case int64:
		return discord.Permissions(value), true
	case int:
		return discord.Permissions(value), true
	default:
		return 0, false
	}
}

func permissionNames(permissions discord.Permissions) string {
	if permissions == 0 {
		return "*(none)*"
	}
	var names []string
	var known discord.Permissions
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

func changedIDs(oldIDs, newIDs []snowflake.ID) (added, removed []snowflake.ID) {
	oldSet := make(map[snowflake.ID]bool, len(oldIDs))
	newSet := make(map[snowflake.ID]bool, len(newIDs))
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
	slices.Sort(added)
	slices.Sort(removed)
	return added, removed
}

func roleLabels(s *bot.Client, guildID string, roleIDs []snowflake.ID) string {
	labels := make([]string, 0, len(roleIDs))
	for _, id := range roleIDs {
		label := fmt.Sprintf("<@&%s> (`%s`)", id, id)
		if s != nil && s.Caches != nil {
			if role, ok := s.Caches.Role(snowflakeID(guildID), id); ok {
				label = fmt.Sprintf("%s (`%s`, %s)", role.Mention(), id, oneLine(role.Name))
			}
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}

type overwriteChange struct {
	id      string
	old     discord.PermissionOverwrite
	current discord.PermissionOverwrite
}

func changedOverwrites(oldOverwrites, newOverwrites []discord.PermissionOverwrite) []overwriteChange {
	oldByID := make(map[string]discord.PermissionOverwrite, len(oldOverwrites))
	newByID := make(map[string]discord.PermissionOverwrite, len(newOverwrites))
	ids := make(map[string]bool, len(oldOverwrites)+len(newOverwrites))
	for _, overwrite := range oldOverwrites {
		if overwrite != nil {
			oldByID[overwrite.ID().String()] = overwrite
			ids[overwrite.ID().String()] = true
		}
	}
	for _, overwrite := range newOverwrites {
		if overwrite != nil {
			newByID[overwrite.ID().String()] = overwrite
			ids[overwrite.ID().String()] = true
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
		oldAllow, oldDeny := overwritePermissions(old)
		newAllow, newDeny := overwritePermissions(current)
		if old == nil || current == nil || oldAllow != newAllow || oldDeny != newDeny || old.Type() != current.Type() {
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
	if overwrite != nil && overwrite.Type() == discord.PermissionOverwriteTypeMember {
		return fmt.Sprintf("<@%s> (`%s`)", change.id, change.id)
	}
	return fmt.Sprintf("<@&%s> (`%s`)", change.id, change.id)
}

func overwritePermissions(overwrite discord.PermissionOverwrite) (discord.Permissions, discord.Permissions) {
	switch overwrite := overwrite.(type) {
	case discord.RolePermissionOverwrite:
		return overwrite.Allow, overwrite.Deny
	case discord.MemberPermissionOverwrite:
		return overwrite.Allow, overwrite.Deny
	default:
		return 0, 0
	}
}

func formatOverwriteChange(change overwriteChange) string {
	oldAllow, oldDeny := overwritePermissions(change.old)
	newAllow, newDeny := overwritePermissions(change.current)
	if change.old == nil {
		return "**Allowed:** " + permissionNames(newAllow) + "\n**Denied:** " + permissionNames(newDeny)
	}
	if change.current == nil {
		return "**Previous allowed:** " + permissionNames(oldAllow) + "\n**Previous denied:** " + permissionNames(oldDeny)
	}
	var lines []string
	appendPermissionDelta := func(label string, old, current discord.Permissions) {
		if added := current &^ old; added != 0 {
			lines = append(lines, fmt.Sprintf("**%s added:** %s", label, permissionNames(added)))
		}
		if removed := old &^ current; removed != 0 {
			lines = append(lines, fmt.Sprintf("**%s removed:** %s", label, permissionNames(removed)))
		}
	}
	appendPermissionDelta("Allowed", oldAllow, newAllow)
	appendPermissionDelta("Denied", oldDeny, newDeny)
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

func (b *Bot) sendEventLog(s *bot.Client, channelID, title, body, filename string) {
	content := "**" + title + "**\n" + body
	message := &discord.MessageCreate{
		Content:         content,
		AllowedMentions: &discord.AllowedMentions{},
		Flags:           discord.MessageFlagSuppressEmbeds,
	}
	if utf8.RuneCountInString(content) > 1950 {
		message.Content = "**" + title + "**\nFull details attached."
		message.Files = []*discord.File{{
			Name:   filename,
			Reader: strings.NewReader(title + "\n\n" + body),
		}}
	}
	if _, err := s.Rest.CreateMessage(snowflakeID(channelID), *message); err != nil && b.Logger != nil {
		b.Logger.Error("failed to send Discord event log", zap.String("channelID", channelID), zap.String("event", title), zap.Error(err))
	}
}
