package discord

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

// handleReactionAdd handles reaction add events for starboard
func (b *Bot) handleReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	// Check if starboard is configured
	if b.Config.StarboardChannelID == "" {
		b.Logger.Debug("starboard reaction ignored - no channel configured",
			zap.String("emoji", r.Emoji.Name))
		return
	}

	// Only process reactions in the configured guild
	if r.GuildID != b.Config.DiscordGuildID {
		b.Logger.Debug("starboard reaction ignored - wrong guild",
			zap.String("guildID", r.GuildID),
			zap.String("expectedGuild", b.Config.DiscordGuildID))
		return
	}

	// Get the emoji to check (default to ⭐ if not configured)
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}

	b.Logger.Debug("starboard processing reaction",
		zap.String("emoji", r.Emoji.Name),
		zap.String("expectedEmoji", starEmoji),
		zap.String("messageID", r.MessageID),
		zap.String("userID", r.UserID),
		zap.String("channelID", r.ChannelID))

	// Check if the reaction is the star emoji
	if r.Emoji.Name != starEmoji {
		b.Logger.Debug("starboard reaction ignored - wrong emoji",
			zap.String("got", r.Emoji.Name),
			zap.String("want", starEmoji))
		return
	}

	// Check if this is a reaction on a starboard message
	isStarboardChannel := r.ChannelID == b.Config.StarboardChannelID

	if isStarboardChannel {
		// Handle star reactions on starboard messages
		b.handleStarboardReaction(s, r)
		return
	}

	// Handle reactions on regular messages (original starboard logic)
	b.handleOriginalMessageReaction(s, r)
}

// handleStarboardReaction handles star reactions on starboard messages
func (b *Bot) handleStarboardReaction(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	// Find the original message ID from the starboard entry
	entry, err := b.DB.GetStarboardEntryByStarboardMsgID(r.MessageID)
	if err != nil {
		b.Logger.Error("starboard failed to get entry by starboard message ID",
			zap.Error(err),
			zap.String("starboardMsgID", r.MessageID))
		return
	}

	if entry == nil {
		b.Logger.Debug("starboard reaction on starboard message - no entry found",
			zap.String("starboardMsgID", r.MessageID))
		return
	}

	// Get the original message to count stars
	msg, err := s.ChannelMessage(entry.ChannelID, entry.OriginalMsgID)
	if err != nil {
		b.Logger.Error("starboard failed to get original message",
			zap.Error(err),
			zap.String("channelID", entry.ChannelID),
			zap.String("messageID", entry.OriginalMsgID))
		return
	}

	// Count total star reactions on the original message
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}
	starCount := countReactions(msg.Reactions, starEmoji)

	b.Logger.Info("starboard reaction on starboard message processed",
		zap.String("starboardMsgID", r.MessageID),
		zap.String("originalMsgID", entry.OriginalMsgID),
		zap.Int("starCount", starCount))

	// Update the entry
	if err := b.DB.UpdateStarboardEntry(entry.OriginalMsgID, starCount, entry.StarboardMsgID); err != nil {
		b.Logger.Error("starboard failed to update entry",
			zap.Error(err),
			zap.String("messageID", entry.OriginalMsgID))
		return
	}

	// Update the starboard message
	if entry.StarboardMsgID != nil {
		b.updateStarboardMessage(s, *entry.StarboardMsgID, msg, starCount)
	}
}

// handleOriginalMessageReaction handles star reactions on original messages
func (b *Bot) handleOriginalMessageReaction(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	// Get the emoji to check (default to ⭐ if not configured)
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}

	// Get the message
	msg, err := s.ChannelMessage(r.ChannelID, r.MessageID)
	if err != nil {
		b.Logger.Error("starboard failed to get message",
			zap.Error(err),
			zap.String("channelID", r.ChannelID),
			zap.String("messageID", r.MessageID))
		return
	}

	b.Logger.Debug("starboard got message",
		zap.String("author", msg.Author.Username),
		zap.String("authorID", msg.Author.ID),
		zap.Bool("isBot", msg.Author.Bot),
		zap.Int("reactionCount", len(msg.Reactions)))

	// Log all reactions for debugging
	for _, reaction := range msg.Reactions {
		b.Logger.Debug("starboard message reaction",
			zap.String("emojiName", reaction.Emoji.Name),
			zap.Int("count", reaction.Count),
			zap.String("emojiID", reaction.Emoji.ID))
	}

	// Don't starboard bot messages (optional - remove if you want to allow bot messages)
	if msg.Author.Bot {
		b.Logger.Debug("starboard ignored - bot message",
			zap.String("authorID", msg.Author.ID))
		return
	}

	// Count total star reactions
	starCount := countReactions(msg.Reactions, starEmoji)

	b.Logger.Info("starboard reaction processed",
		zap.String("messageID", r.MessageID),
		zap.Int("starCount", starCount),
		zap.Int("totalReactions", len(msg.Reactions)),
		zap.String("emoji", starEmoji))

	// Check if message is already in starboard
	entry, err := b.DB.GetStarboardEntry(r.MessageID)
	if err != nil {
		b.Logger.Error("starboard failed to get entry from DB",
			zap.Error(err),
			zap.String("messageID", r.MessageID))
		return
	}

	threshold := b.Config.StarboardThreshold
	if threshold == 0 {
		threshold = 3 // Default threshold
	}

	b.Logger.Debug("starboard threshold check",
		zap.Int("starCount", starCount),
		zap.Int("threshold", threshold),
		zap.Bool("hasEntry", entry != nil))

	if entry == nil {
		// New entry - check if threshold is met
		if starCount >= threshold {
			b.Logger.Info("starboard threshold reached - creating entry",
				zap.String("messageID", r.MessageID),
				zap.Int("starCount", starCount),
				zap.Int("threshold", threshold))

			// Create starboard entry and post
			content := msg.Content
			if len(content) > 1000 {
				content = content[:997] + "..."
			}

			entry, err = b.DB.AddStarboardEntry(
				r.MessageID,
				r.ChannelID,
				r.GuildID,
				msg.Author.ID,
				content,
				starCount,
				time.Now().Unix(),
			)
			if err != nil {
				b.Logger.Error("starboard failed to add entry to DB",
					zap.Error(err),
					zap.String("messageID", r.MessageID))
				return
			}

			b.Logger.Info("starboard entry created in DB",
				zap.Int64("entryID", entry.ID),
				zap.String("originalMsgID", entry.OriginalMsgID))

			// Post to starboard channel
			starboardMsgID, err := b.postToStarboard(s, msg, starCount)
			if err != nil {
				b.Logger.Error("starboard failed to post to channel",
					zap.Error(err),
					zap.String("channelID", b.Config.StarboardChannelID))
				return
			}

			b.Logger.Info("starboard message posted",
				zap.String("starboardMsgID", starboardMsgID),
				zap.String("channelID", b.Config.StarboardChannelID))

			// Update entry with starboard message ID
			if err := b.DB.UpdateStarboardEntry(r.MessageID, starCount, &starboardMsgID); err != nil {
				b.Logger.Error("starboard failed to update entry with starboard message ID",
					zap.Error(err),
					zap.String("messageID", r.MessageID))
			}
		} else {
			b.Logger.Debug("starboard threshold not reached yet",
				zap.String("messageID", r.MessageID),
				zap.Int("starCount", starCount),
				zap.Int("threshold", threshold))
		}
	} else {
		b.Logger.Info("starboard updating existing entry",
			zap.String("messageID", r.MessageID),
			zap.Int64("entryID", entry.ID),
			zap.Int("newStarCount", starCount),
			zap.String("existingStarboardMsgID", func() string {
				if entry.StarboardMsgID != nil {
					return *entry.StarboardMsgID
				}
				return "nil"
			}()))

		// Update existing entry
		if err := b.DB.UpdateStarboardEntry(r.MessageID, starCount, entry.StarboardMsgID); err != nil {
			b.Logger.Error("starboard failed to update entry",
				zap.Error(err),
				zap.String("messageID", r.MessageID))
			return
		}

		// Update the starboard message
		if entry.StarboardMsgID != nil {
			b.updateStarboardMessage(s, *entry.StarboardMsgID, msg, starCount)
		} else {
			b.Logger.Warn("starboard entry exists but has no starboard message ID",
				zap.String("messageID", r.MessageID))
		}
	}
}

// handleReactionRemove handles reaction remove events for starboard
func (b *Bot) handleReactionRemove(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
	// Check if starboard is configured
	if b.Config.StarboardChannelID == "" {
		return
	}

	// Only process reactions in the configured guild
	if r.GuildID != b.Config.DiscordGuildID {
		return
	}

	// Get the emoji to check (default to ⭐ if not configured)
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}

	// Check if the reaction is the star emoji
	if r.Emoji.Name != starEmoji {
		return
	}

	// Check if this is a reaction removal on a starboard message
	isStarboardChannel := r.ChannelID == b.Config.StarboardChannelID

	if isStarboardChannel {
		b.handleStarboardReactionRemove(s, r)
		return
	}

	b.handleOriginalMessageReactionRemove(s, r)
}

// handleStarboardReactionRemove handles reaction removal on starboard messages
func (b *Bot) handleStarboardReactionRemove(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
	// Find the original message ID from the starboard entry
	entry, err := b.DB.GetStarboardEntryByStarboardMsgID(r.MessageID)
	if err != nil {
		b.Logger.Error("starboard failed to get entry by starboard message ID",
			zap.Error(err),
			zap.String("starboardMsgID", r.MessageID))
		return
	}

	if entry == nil {
		b.Logger.Debug("starboard reaction removal on starboard message - no entry found",
			zap.String("starboardMsgID", r.MessageID))
		return
	}

	// Get the original message to count stars
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}

	msg, err := s.ChannelMessage(entry.ChannelID, entry.OriginalMsgID)
	if err != nil {
		b.Logger.Error("starboard failed to get original message for removal",
			zap.Error(err),
			zap.String("channelID", entry.ChannelID),
			zap.String("messageID", entry.OriginalMsgID))
		return
	}

	starCount := countReactions(msg.Reactions, starEmoji)

	threshold := b.Config.StarboardThreshold
	if threshold == 0 {
		threshold = 3
	}

	if starCount < threshold {
		b.Logger.Info("starboard removing entry - below threshold",
			zap.String("messageID", entry.OriginalMsgID),
			zap.Int("starCount", starCount),
			zap.Int("threshold", threshold))
		// Remove from starboard
		if entry.StarboardMsgID != nil {
			s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID)
		}
		b.DB.DeleteStarboardEntry(entry.OriginalMsgID)
	} else {
		// Update existing entry
		b.Logger.Info("starboard updating count after removal",
			zap.String("messageID", entry.OriginalMsgID),
			zap.Int("starCount", starCount))
		if err := b.DB.UpdateStarboardEntry(entry.OriginalMsgID, starCount, entry.StarboardMsgID); err != nil {
			b.Logger.Error("starboard failed to update entry after removal",
				zap.Error(err),
				zap.String("messageID", entry.OriginalMsgID))
			return
		}

		// Update the starboard message
		if entry.StarboardMsgID != nil {
			b.updateStarboardMessage(s, *entry.StarboardMsgID, msg, starCount)
		}
	}
}

// handleOriginalMessageReactionRemove handles reaction removal on original messages
func (b *Bot) handleOriginalMessageReactionRemove(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
	// Get the emoji to check (default to ⭐ if not configured)
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}

	b.Logger.Debug("starboard processing reaction removal",
		zap.String("messageID", r.MessageID),
		zap.String("userID", r.UserID))

	// Get the message
	msg, err := s.ChannelMessage(r.ChannelID, r.MessageID)
	if err != nil {
		// Message might be deleted, try to get from database
		entry, dbErr := b.DB.GetStarboardEntry(r.MessageID)
		if dbErr != nil || entry == nil {
			b.Logger.Debug("starboard reaction removal ignored - message deleted and not in starboard",
				zap.String("messageID", r.MessageID))
			return
		}

		// Update star count to reflect removal (we don't know exact count, so decrement by 1)
		newCount := entry.StarCount - 1
		if newCount < 0 {
			newCount = 0
		}

		threshold := b.Config.StarboardThreshold
		if threshold == 0 {
			threshold = 3
		}

		if newCount < threshold && entry.StarboardMsgID != nil {
			// Remove from starboard
			b.Logger.Info("starboard removing entry - below threshold after message deletion",
				zap.String("messageID", r.MessageID),
				zap.Int("newCount", newCount),
				zap.Int("threshold", threshold))
			s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID)
			b.DB.DeleteStarboardEntry(r.MessageID)
		} else {
			b.DB.UpdateStarboardEntry(r.MessageID, newCount, entry.StarboardMsgID)
			if entry.StarboardMsgID != nil {
				// Update message with estimated count
				b.updateStarboardMessage(s, *entry.StarboardMsgID, nil, newCount)
			}
		}
		return
	}

	// Count total star reactions
	starCount := countReactions(msg.Reactions, starEmoji)

	b.Logger.Debug("starboard reaction removal counted stars",
		zap.String("messageID", r.MessageID),
		zap.Int("starCount", starCount))

	// Get existing entry
	entry, err := b.DB.GetStarboardEntry(r.MessageID)
	if err != nil {
		b.Logger.Error("starboard failed to get entry for removal",
			zap.Error(err),
			zap.String("messageID", r.MessageID))
		return
	}

	if entry == nil {
		b.Logger.Debug("starboard reaction removal ignored - message not in starboard",
			zap.String("messageID", r.MessageID))
		return // Not in starboard yet
	}

	threshold := b.Config.StarboardThreshold
	if threshold == 0 {
		threshold = 3
	}

	if starCount < threshold {
		b.Logger.Info("starboard removing entry - below threshold",
			zap.String("messageID", r.MessageID),
			zap.Int("starCount", starCount),
			zap.Int("threshold", threshold))
		// Remove from starboard
		if entry.StarboardMsgID != nil {
			s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID)
		}
		b.DB.DeleteStarboardEntry(r.MessageID)
	} else {
		// Update existing entry
		b.Logger.Info("starboard updating count after removal",
			zap.String("messageID", r.MessageID),
			zap.Int("starCount", starCount))
		if err := b.DB.UpdateStarboardEntry(r.MessageID, starCount, entry.StarboardMsgID); err != nil {
			b.Logger.Error("starboard failed to update entry after removal",
				zap.Error(err),
				zap.String("messageID", r.MessageID))
			return
		}

		// Update the starboard message
		if entry.StarboardMsgID != nil {
			b.updateStarboardMessage(s, *entry.StarboardMsgID, msg, starCount)
		}
	}
}

// handleMessageDelete handles message deletion events to clean up starboard
func (b *Bot) handleMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	// Check if starboard is configured
	if b.Config.StarboardChannelID == "" {
		return
	}

	b.Logger.Debug("starboard checking deleted message",
		zap.String("messageID", m.ID))

	// Check if this message is in starboard
	entry, err := b.DB.GetStarboardEntry(m.ID)
	if err != nil {
		b.Logger.Error("starboard failed to check deleted message",
			zap.Error(err),
			zap.String("messageID", m.ID))
		return
	}

	if entry == nil {
		b.Logger.Debug("starboard deleted message not in starboard",
			zap.String("messageID", m.ID))
		return
	}

	b.Logger.Info("starboard cleaning up deleted message",
		zap.String("messageID", m.ID),
		zap.String("starboardMsgID", func() string {
			if entry.StarboardMsgID != nil {
				return *entry.StarboardMsgID
			}
			return "nil"
		}()))

	// Delete the starboard message
	if entry.StarboardMsgID != nil {
		s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID)
	}

	// Remove from database
	b.DB.DeleteStarboardEntry(m.ID)
}

// countReactions counts the total number of a specific emoji reaction
func countReactions(reactions []*discordgo.MessageReactions, emoji string) int {
	for _, r := range reactions {
		// Check for both custom and unicode emojis
		emojiMatch := r.Emoji.Name == emoji
		if r.Emoji.ID != "" {
			// Custom emoji - compare by ID if needed, but usually name works
			emojiMatch = r.Emoji.Name == emoji || r.Emoji.ID == emoji
		}
		if emojiMatch {
			return r.Count
		}
	}
	return 0
}

// postToStarboard posts a message to the starboard channel
func (b *Bot) postToStarboard(s *discordgo.Session, msg *discordgo.Message, starCount int) (string, error) {
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}

	// Build embed
	description := msg.Content
	if description == "" && len(msg.Embeds) > 0 {
		description = "*[Message contains embeds]*"
	}

	embed := &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			Name:    msg.Author.Username,
			IconURL: msg.Author.AvatarURL(""),
		},
		Description: description,
		Color:       0xFFD700, // Gold color
		Timestamp:   msg.Timestamp.Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("#%s", msg.ChannelID),
		},
	}

	// Add image if message has attachments
	if len(msg.Attachments) > 0 {
		for _, att := range msg.Attachments {
			if isImage(att.ContentType) {
				embed.Image = &discordgo.MessageEmbedImage{
					URL: att.URL,
				}
				break
			}
		}
	}

	// Create message content with star count and link to original message
	messageURL := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", b.Config.DiscordGuildID, msg.ChannelID, msg.ID)
	content := fmt.Sprintf("%s %d | <%s>", starEmoji, starCount, messageURL)

	b.Logger.Info("posting to starboard",
		zap.String("channelID", b.Config.StarboardChannelID),
		zap.String("author", msg.Author.Username),
		zap.Int("starCount", starCount))

	// Send to starboard channel
	starboardMsg, err := s.ChannelMessageSendComplex(b.Config.StarboardChannelID, &discordgo.MessageSend{
		Content: content,
		Embeds:  []*discordgo.MessageEmbed{embed},
	})
	if err != nil {
		return "", err
	}

	return starboardMsg.ID, nil
}

// updateStarboardMessage updates an existing starboard message
func (b *Bot) updateStarboardMessage(s *discordgo.Session, starboardMsgID string, msg *discordgo.Message, starCount int) {
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}

	// Create content with star count and link to original message
	var messageURL string
	if msg != nil {
		messageURL = fmt.Sprintf("https://discord.com/channels/%s/%s/%s", b.Config.DiscordGuildID, msg.ChannelID, msg.ID)
	} else {
		// If we don't have the message, try to get it from the database
		entry, err := b.DB.GetStarboardEntryByStarboardMsgID(starboardMsgID)
		if err == nil && entry != nil {
			messageURL = fmt.Sprintf("https://discord.com/channels/%s/%s/%s", entry.GuildID, entry.ChannelID, entry.OriginalMsgID)
		}
	}

	var content string
	if messageURL != "" {
		content = fmt.Sprintf("%s %d | <%s>", starEmoji, starCount, messageURL)
	} else {
		content = fmt.Sprintf("%s %d", starEmoji, starCount)
	}

	b.Logger.Debug("updating starboard message",
		zap.String("starboardMsgID", starboardMsgID),
		zap.Int("newStarCount", starCount))

	// Edit the message content (to update star count)
	s.ChannelMessageEdit(b.Config.StarboardChannelID, starboardMsgID, content)
}

// RefreshAllStarboard refreshes all starboard entries by rechecking their star counts
func (b *Bot) RefreshAllStarboard(s *discordgo.Session) error {
	entries, err := b.DB.GetAllStarboardEntries()
	if err != nil {
		return fmt.Errorf("failed to get all starboard entries: %w", err)
	}

	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = "⭐"
	}

	threshold := b.Config.StarboardThreshold
	if threshold == 0 {
		threshold = 3
	}

	b.Logger.Info("refreshing all starboard entries",
		zap.Int("count", len(entries)))

	for _, entry := range entries {
		// Get the original message to count stars
		msg, err := s.ChannelMessage(entry.ChannelID, entry.OriginalMsgID)
		if err != nil {
			b.Logger.Error("starboard refresh failed to get message",
				zap.Error(err),
				zap.String("channelID", entry.ChannelID),
				zap.String("messageID", entry.OriginalMsgID))
			continue
		}

		// Count stars on the original message
		originalStarCount := countReactions(msg.Reactions, starEmoji)

		// Also count stars on the starboard message itself
		starboardStarCount := 0
		if entry.StarboardMsgID != nil {
			starboardMsg, err := s.ChannelMessage(b.Config.StarboardChannelID, *entry.StarboardMsgID)
			if err == nil {
				starboardStarCount = countReactions(starboardMsg.Reactions, starEmoji)
				b.Logger.Debug("counted stars on starboard message",
					zap.String("starboardMsgID", *entry.StarboardMsgID),
					zap.Int("starboardStars", starboardStarCount))
			} else {
				b.Logger.Warn("failed to get starboard message for star counting",
					zap.Error(err),
					zap.String("starboardMsgID", *entry.StarboardMsgID))
			}
		}

		// Total star count includes stars on both messages
		starCount := originalStarCount + starboardStarCount

		b.Logger.Info("starboard refresh counted stars",
			zap.String("messageID", entry.OriginalMsgID),
			zap.Int("originalStars", originalStarCount),
			zap.Int("starboardStars", starboardStarCount),
			zap.Int("totalStars", starCount))

		if starCount < threshold {
			// Remove from starboard
			b.Logger.Info("starboard refresh removing entry - below threshold",
				zap.String("messageID", entry.OriginalMsgID),
				zap.Int("starCount", starCount),
				zap.Int("threshold", threshold))
			if entry.StarboardMsgID != nil {
				s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID)
			}
			b.DB.DeleteStarboardEntry(entry.OriginalMsgID)
		} else {
			// Update the entry and message
			b.Logger.Info("starboard refresh updating entry",
				zap.String("messageID", entry.OriginalMsgID),
				zap.Int("starCount", starCount))
			if err := b.DB.UpdateStarboardEntry(entry.OriginalMsgID, starCount, entry.StarboardMsgID); err != nil {
				b.Logger.Error("starboard refresh failed to update entry",
					zap.Error(err),
					zap.String("messageID", entry.OriginalMsgID))
				continue
			}

			if entry.StarboardMsgID != nil {
				b.updateStarboardMessage(s, *entry.StarboardMsgID, msg, starCount)
			}
		}
	}

	return nil
}

// isImage checks if a content type is an image
func isImage(contentType string) bool {
	return strings.HasPrefix(contentType, "image/")
}
