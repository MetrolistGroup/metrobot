package discord

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// garminReadableChannels returns the configured alias-to-channel map with
// empty aliases and IDs removed.
func (b *Bot) garminReadableChannels() map[string]string {
	channels := map[string]string{}
	for alias, channelID := range b.discordSettings().AI.ReadableChannels {
		alias = normalizeGarminChannelAlias(alias)
		if alias != "" && snowflakeID(channelID) != 0 {
			channels[alias] = channelID
		}
	}
	return channels
}

func normalizeGarminChannelAlias(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")
	return strings.ReplaceAll(name, " ", "-")
}

func (b *Bot) readGarminCommunityChannel(s *bot.Client, channelName, query string, limit int) (string, error) {
	return b.readGarminCommunityChannelContext(context.Background(), s, channelName, query, limit)
}

func (b *Bot) readGarminCommunityChannelContext(ctx context.Context, s *bot.Client, channelName, query string, limit int) (string, error) {
	channels := b.garminReadableChannels()
	channelName = normalizeGarminChannelAlias(channelName)
	channelID, ok := channels[channelName]
	if !ok {
		channelID, ok = channels[strings.ReplaceAll(channelName, "-", "")]
	}
	if !ok {
		return "", fmt.Errorf("unknown readable channel %q; available channels: %s", channelName, strings.Join(slices.Sorted(maps.Keys(channels)), ", "))
	}
	return b.readGarminChannelMessagesContext(ctx, s, channelID, "", query, min(limit, 25), garminAIToolResultSize)
}

// garminCommunityChannelTool describes only the configured readable channels.
func (b *Bot) garminCommunityChannelTool() (cmd.GarminAITool, bool) {
	channels := b.garminReadableChannels()
	if len(channels) == 0 {
		return cmd.GarminAITool{}, false
	}
	aliases := slices.Sorted(maps.Keys(channels))
	described := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if description := b.garminChannelDescription(channels[alias]); description != "" {
			alias += " (" + description + ")"
		}
		described = append(described, alias)
	}
	schema := mustJSON(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"channel": map[string]any{"type": "string", "enum": aliases},
			"query":   map[string]any{"type": "string", "description": "Optional case-insensitive text to find within the latest 100 messages"},
			"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 25, "description": "Maximum messages to return; defaults to 15"},
		},
		"required":             []string{"channel"},
		"additionalProperties": false,
	})
	return garminTool("read_community_channel", "Read recent messages from approved channels: "+strings.Join(described, "; ")+". Optionally search within the latest 100 messages.", schema), true
}

// garminReadableChannelForRequest keeps a routed alias only when it is configured.
func (b *Bot) garminReadableChannelForRequest(messages []cmd.GarminAIMessage) string {
	alias := garminReadableChannelForConversation(messages)
	if _, ok := b.garminReadableChannels()[alias]; !ok {
		return ""
	}
	return alias
}

func confirmGarminHistoryAccess(ctx context.Context, s *bot.Client, m *discord.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !garminCanReadChannelHistory(s, m) || s.Rest == nil {
		return fmt.Errorf("channel history requires confirmed View Channel and Read Message History permissions")
	}
	// The triggering member and member cache can both predate a role revocation.
	member, err := s.Rest.GetMember(*m.GuildID, m.Author.ID, rest.WithCtx(ctx))
	if err != nil {
		return fmt.Errorf("confirming current channel-history permissions: %w", err)
	}
	current := *m
	current.Member = member
	if member == nil || member.User.ID != m.Author.ID || !garminCanReadChannelHistory(s, &current) {
		return fmt.Errorf("current channel-history permissions could not be confirmed")
	}
	return nil
}

func garminCanReadChannelHistory(s *bot.Client, m *discord.Message) bool {
	if s == nil || s.Rest == nil || s.Caches == nil || m == nil || m.GuildID == nil || m.Author.ID == 0 || m.ID == 0 {
		return false
	}
	guildID := *m.GuildID
	channel, ok := s.Caches.Channel(m.ChannelID)
	if !ok || channel.GuildID() != guildID {
		return false
	}
	switch channel.Type() {
	case discord.ChannelTypeGuildPublicThread, discord.ChannelTypeGuildPrivateThread, discord.ChannelTypeGuildNewsThread:
		if channel.ParentID() == nil {
			return false
		}
		channel, ok = s.Caches.Channel(*channel.ParentID())
		if !ok || channel.GuildID() != guildID {
			return false
		}
	}
	var member discord.Member
	if m.Member != nil {
		member = *m.Member
	} else {
		var cached bool
		member, cached = s.Caches.Member(guildID, m.Author.ID)
		if !cached {
			return false
		}
	}
	member.GuildID = guildID
	member.User = m.Author
	if _, ok := s.Caches.Guild(guildID); !ok {
		return false
	}
	if _, ok := s.Caches.Role(guildID, guildID); !ok {
		return false
	}
	for _, roleID := range member.RoleIDs {
		if _, ok := s.Caches.Role(guildID, roleID); !ok {
			return false
		}
	}
	permissions := s.Caches.MemberPermissionsInChannel(channel, member)
	if !permissions.Has(discord.PermissionAdministrator) {
		// Member overwrites take precedence over combined role overwrites.
		if overwrite, ok := channel.PermissionOverwrites().Member(m.Author.ID); ok {
			permissions = permissions.Remove(overwrite.Deny).Add(overwrite.Allow)
		}
	}
	return permissions.Has(discord.PermissionViewChannel, discord.PermissionReadMessageHistory)
}

func (b *Bot) readGarminChannelMessages(s *bot.Client, channelID, beforeID, query string, limit int) (string, error) {
	return b.readGarminChannelMessagesContext(context.Background(), s, channelID, beforeID, query, min(limit, 25), garminAIToolResultSize)
}

func (b *Bot) readGarminChannelMessagesContext(ctx context.Context, s *bot.Client, channelID, beforeID, query string, limit, budget int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s == nil || s.Rest == nil {
		return "", fmt.Errorf("Discord session is unavailable")
	}
	query = strings.TrimSpace(query)
	if len([]rune(query)) > 500 {
		return "", fmt.Errorf("history text filter must be at most 500 characters")
	}
	channelName := channelID
	guildID := ""
	if b.Config != nil {
		guildID = b.Config.DiscordGuildID
	}
	if s.Caches != nil {
		if channel, ok := s.Caches.Channel(snowflakeID(channelID)); ok {
			if channel.Name() != "" {
				channelName = channel.Name()
			}
			if guildID == "" {
				guildID = channel.GuildID().String()
			}
		}
	}
	if limit <= 0 {
		limit = 15
	}
	limit = min(limit, 100)
	fetchLimit := limit
	if query != "" {
		fetchLimit = 100
	}
	messages, err := s.Rest.GetMessages(snowflakeID(channelID), 0, snowflakeID(beforeID), 0, fetchLimit, rest.WithCtx(ctx))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", channelName, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Use embedded/cache identity only: history must not do a member REST lookup per author.
	rolesByID := map[snowflake.ID]discord.Role{}
	if s.Caches != nil {
		for role := range s.Caches.Roles(snowflakeID(guildID)) {
			rolesByID[role.ID] = role
		}
	}
	slices.SortFunc(messages, func(a, b discord.Message) int { return cmp.Compare(b.ID, a.ID) })
	queryLower := strings.ToLower(query)
	participants := map[string]map[string]any{}
	results := make([]map[string]any, 0, min(limit, len(messages)))
	matched := 0
	fieldsTruncated := false
	bound := func(text string, size int) string {
		bounded := truncateRunes(text, size)
		fieldsTruncated = fieldsTruncated || bounded != text
		return bounded
	}
	channelName = bound(channelName, 128)
	for _, message := range messages[:min(len(messages), fetchLimit)] {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if message.ChannelID.String() != channelID || message.ID == 0 || (beforeID != "" && message.ID >= snowflakeID(beforeID)) || !b.garminMessageVisible(channelID, message.ID.String()) || (queryLower != "" && !strings.Contains(strings.ToLower(message.Content), queryLower)) {
			continue
		}
		matched++
		if len(results) == limit {
			continue
		}
		content := bound(message.Content, 4000)
		result := map[string]any{
			"id":                message.ID.String(),
			"content":           content,
			"content_truncated": content != message.Content,
			"timestamp":         message.CreatedAt.UTC().Format(time.RFC3339),
		}
		if emojis := garminMessageEmojis(&message); len(emojis) > 0 {
			result["emojis"] = emojis
		}
		if snowflakeID(guildID) != 0 {
			result["url"] = discordMessageURL(guildID, channelID, message.ID.String())
		}
		if reference := message.MessageReference; reference != nil && reference.MessageID != nil {
			if (reference.ChannelID == nil || *reference.ChannelID == message.ChannelID) && b.garminMessageVisible(channelID, reference.MessageID.String()) {
				result["reply_to_message_id"] = reference.MessageID.String()
			} else {
				result["reply_context_unavailable"] = true
			}
		}
		if message.Author.ID != 0 {
			authorID := message.Author.ID.String()
			result["author_id"] = authorID
			if _, exists := participants[authorID]; !exists {
				member := message.Member
				if member == nil && s.Caches != nil {
					if cached, ok := s.Caches.Member(snowflakeID(guildID), message.Author.ID); ok {
						member = &cached
					}
				}
				if member != nil && len(member.RoleIDs) > 25 {
					copy := *member
					copy.RoleIDs = member.RoleIDs[:25]
					member = &copy
					fieldsTruncated = true
				}
				identity := garminDiscordMemberContext(&message.Author, member, rolesByID)
				for key, item := range identity {
					if text, ok := item.(string); ok {
						identity[key] = bound(text, 128)
					}
				}
				if roles, ok := identity["roles"].([]map[string]string); ok {
					for _, role := range roles {
						if name, known := role["name"]; known {
							role["name"] = bound(name, 128)
						}
					}
				}
				participants[authorID] = identity
			}
		}
		if len(message.Attachments) > 0 {
			result["attachment_count"] = len(message.Attachments)
			result["attachments_truncated"] = len(message.Attachments) > 10
			fieldsTruncated = fieldsTruncated || len(message.Attachments) > 10
			attachments := make([]map[string]string, 0, min(len(message.Attachments), 10))
			for _, attachment := range message.Attachments[:min(len(message.Attachments), 10)] {
				attachments = append(attachments, map[string]string{
					"filename":     bound(attachment.Filename, 256),
					"content_type": bound(value(attachment.ContentType), 128),
					"url":          bound(attachment.URL, 2048),
				})
			}
			result["attachments"] = attachments
		}
		results = append(results, result)
	}
	slices.Reverse(results)
	encode := func() string {
		usedParticipants := map[string]map[string]any{}
		for _, message := range results {
			if authorID, _ := message["author_id"].(string); authorID != "" {
				usedParticipants[authorID] = participants[authorID]
			}
		}
		return mustJSON(map[string]any{
			"channel":              channelName,
			"channel_id":           channelID,
			"before_id":            beforeID,
			"query":                query,
			"requested_limit":      limit,
			"fetch_limit":          fetchLimit,
			"byte_budget":          budget,
			"fetched_count":        len(messages),
			"matched_count":        matched,
			"returned_count":       len(results),
			"omitted_count":        matched - len(results),
			"fewer_than_requested": len(results) < limit,
			"fields_truncated":     fieldsTruncated,
			"truncated":            fieldsTruncated || len(results) < matched,
			"coverage":             "Only available messages in this fetch after any reset; before_id is exclusive when set. Deleted messages are unavailable. A text filter scans at most fetch_limit messages.",
			"participants":         usedParticipants,
			"messages":             results,
		})
	}
	encoded := encode()
	for len(encoded) > budget && len(results) > 0 {
		results = results[1:]
		encoded = encode()
	}
	return encoded, nil
}
