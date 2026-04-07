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

	// Get the message
	msg, err := s.ChannelMessage(r.ChannelID, r.MessageID)
	if err != nil {
		b.Logger.Error("failed to get message for starboard", zap.Error(err))
		return
	}

	// Don't starboard bot messages (optional - remove if you want to allow bot messages)
	if msg.Author.Bot {
		return
	}

	// Count total star reactions
	starCount := countReactions(msg.Reactions, starEmoji)

	// Check if message is already in starboard
	entry, err := b.DB.GetStarboardEntry(r.MessageID)
	if err != nil {
		b.Logger.Error("failed to get starboard entry", zap.Error(err))
		return
	}

	threshold := b.Config.StarboardThreshold
	if threshold == 0 {
		threshold = 3 // Default threshold
	}

	if entry == nil {
		// New entry - check if threshold is met
		if starCount >= threshold {
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
				b.Logger.Error("failed to add starboard entry", zap.Error(err))
				return
			}

			// Post to starboard channel
			starboardMsgID, err := b.postToStarboard(s, msg, starCount)
			if err != nil {
				b.Logger.Error("failed to post to starboard", zap.Error(err))
				return
			}

			// Update entry with starboard message ID
			if err := b.DB.UpdateStarboardEntry(r.MessageID, starCount, &starboardMsgID); err != nil {
				b.Logger.Error("failed to update starboard entry", zap.Error(err))
			}
		}
	} else {
		// Update existing entry
		if err := b.DB.UpdateStarboardEntry(r.MessageID, starCount, entry.StarboardMsgID); err != nil {
			b.Logger.Error("failed to update starboard entry", zap.Error(err))
			return
		}

		// Update the starboard message
		if entry.StarboardMsgID != nil {
			b.updateStarboardMessage(s, *entry.StarboardMsgID, msg, starCount)
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

	// Get the message
	msg, err := s.ChannelMessage(r.ChannelID, r.MessageID)
	if err != nil {
		// Message might be deleted, try to get from database
		entry, dbErr := b.DB.GetStarboardEntry(r.MessageID)
		if dbErr != nil || entry == nil {
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

	// Get existing entry
	entry, err := b.DB.GetStarboardEntry(r.MessageID)
	if err != nil {
		b.Logger.Error("failed to get starboard entry", zap.Error(err))
		return
	}

	if entry == nil {
		return // Not in starboard yet
	}

	threshold := b.Config.StarboardThreshold
	if threshold == 0 {
		threshold = 3
	}

	if starCount < threshold {
		// Remove from starboard
		if entry.StarboardMsgID != nil {
			s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID)
		}
		b.DB.DeleteStarboardEntry(r.MessageID)
	} else {
		// Update existing entry
		if err := b.DB.UpdateStarboardEntry(r.MessageID, starCount, entry.StarboardMsgID); err != nil {
			b.Logger.Error("failed to update starboard entry", zap.Error(err))
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

	// Check if this message is in starboard
	entry, err := b.DB.GetStarboardEntry(m.ID)
	if err != nil {
		b.Logger.Error("failed to get starboard entry", zap.Error(err))
		return
	}

	if entry == nil {
		return
	}

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

	// Edit the message content (to update star count)
	s.ChannelMessageEdit(b.Config.StarboardChannelID, starboardMsgID, content)
}

// isImage checks if a content type is an image
func isImage(contentType string) bool {
	return strings.HasPrefix(contentType, "image/")
}
