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

	// Don't process reactions in the starboard channel itself
	if r.ChannelID == b.Config.StarboardChannelID {
		b.Logger.Debug("starboard reaction ignored - reaction in starboard channel",
			zap.String("channelID", r.ChannelID))
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
		zap.String("userID", r.UserID))

	// Check if the reaction is the star emoji
	if r.Emoji.Name != starEmoji {
		b.Logger.Debug("starboard reaction ignored - wrong emoji",
			zap.String("got", r.Emoji.Name),
			zap.String("want", starEmoji))
		return
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

	// Don't process reactions in the starboard channel itself
	if r.ChannelID == b.Config.StarboardChannelID {
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
		if r.Emoji.Name == emoji {
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

	// Create message content with star count
	content := fmt.Sprintf("%s %d", starEmoji, starCount)

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

	content := fmt.Sprintf("%s %d", starEmoji, starCount)

	b.Logger.Debug("updating starboard message",
		zap.String("starboardMsgID", starboardMsgID),
		zap.Int("newStarCount", starCount))

	// Edit the message content (to update star count)
	s.ChannelMessageEdit(b.Config.StarboardChannelID, starboardMsgID, content)
}

// isImage checks if a content type is an image
func isImage(contentType string) bool {
	return strings.HasPrefix(contentType, "image/")
}
