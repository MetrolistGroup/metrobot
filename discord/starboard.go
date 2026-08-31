package discord

import (
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const (
	prohibitedFlagReaction              = "🇮🇱"
	prohibitedStarOfDavidReaction       = "✡️"
	prohibitedStarOfDavidTextReaction   = "✡"
	prohibitedFlagReactionReason        = "using prohibited :flag_il: reaction"
	prohibitedStarOfDavidReactionReason = "using prohibited :star_of_david: reaction"
	defaultStarboardEmoji               = "⭐"
	sobboardEmoji                       = "😭"
	defaultBoardThreshold               = 3
)

var ignoredBoardCategories = map[string]struct{}{
	"1441100425595195442": {},
	"1417289207944183939": {},
	"1471523726947254272": {},
	"1462269835756175462": {},
}

type reactionBoard struct {
	name  string
	emoji string
}

type prohibitedReactionScanResult struct {
	MessagesChecked  int
	ReactionsFound   int
	ReactionsRemoved int
	WarningsIssued   int
	WarningsSkipped  int
	FetchFailures    int
	RemoveFailures   int
	WarnFailures     int
}

func prohibitedReactionReason(emojiName string) (string, bool) {
	switch emojiName {
	case prohibitedFlagReaction:
		return prohibitedFlagReactionReason, true
	case prohibitedStarOfDavidReaction, prohibitedStarOfDavidTextReaction:
		return prohibitedStarOfDavidReactionReason, true
	default:
		return "", false
	}
}

func isSessionUser(s *discordgo.Session, userID string) bool {
	return s.State != nil && s.State.User != nil && userID == s.State.User.ID
}

func (b *Bot) reactionBoards() []reactionBoard {
	starEmoji := b.Config.StarboardEmoji
	if starEmoji == "" {
		starEmoji = defaultStarboardEmoji
	}
	return []reactionBoard{
		{name: "starboard", emoji: starEmoji},
		{name: "sobboard", emoji: sobboardEmoji},
	}
}

func (b *Bot) reactionBoard(emoji string) (reactionBoard, bool) {
	for _, board := range b.reactionBoards() {
		if board.emoji == emoji {
			return board, true
		}
	}
	return reactionBoard{}, false
}

func (b *Bot) boardThreshold() int {
	if b.Config.StarboardThreshold > 0 {
		return b.Config.StarboardThreshold
	}
	return defaultBoardThreshold
}

// handleReactionAdd handles reaction add events for starboard and sobboard.
func (b *Bot) handleReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	if b.handleProhibitedReaction(s, r) {
		return
	}
	if b.Config.StarboardChannelID == "" || r.GuildID != b.Config.DiscordGuildID {
		return
	}

	board, ok := b.reactionBoard(r.Emoji.Name)
	if !ok {
		return
	}
	if r.ChannelID == b.Config.StarboardChannelID {
		b.handleBoardMessageReaction(s, r.MessageID, board)
		return
	}
	if b.ignoreBoardChannel(s, r.ChannelID) {
		return
	}
	b.handleOriginalMessageReaction(s, r.ChannelID, r.MessageID, r.GuildID, board)
}

func (b *Bot) handleProhibitedReaction(s *discordgo.Session, r *discordgo.MessageReactionAdd) bool {
	reason, prohibited := prohibitedReactionReason(r.Emoji.Name)
	if r.GuildID != b.Config.DiscordGuildID || r.UserID == "" || !prohibited {
		return false
	}
	if isSessionUser(s, r.UserID) {
		return true
	}

	b.removeAndWarnProhibitedReaction(s, r.ChannelID, r.MessageID, r.UserID, r.Emoji.APIName(), reason)
	return true
}

func (b *Bot) removeAndWarnProhibitedReaction(s *discordgo.Session, channelID, messageID, userID, emojiAPIName, reason string) (bool, bool, bool) {
	removed := true
	if err := s.MessageReactionRemove(channelID, messageID, emojiAPIName, userID); err != nil {
		removed = false
		b.Logger.Warn("failed to remove prohibited reaction",
			zap.Error(err),
			zap.String("messageID", messageID),
			zap.String("userID", userID))
	}

	if b.DB.IsAdmin("discord", userID, b.Config) {
		return removed, false, true
	}

	warned := true
	_, _, _, err := b.Warn.Warn(b.newBanner(), "system", userID, reason, b.Config)
	if err != nil {
		warned = false
		b.Logger.Error("failed to warn for prohibited reaction",
			zap.Error(err),
			zap.String("messageID", messageID),
			zap.String("userID", userID))
	}

	return removed, warned, false
}

func (b *Bot) scanProhibitedReactions(s *discordgo.Session, channelID string) (prohibitedReactionScanResult, error) {
	var result prohibitedReactionScanResult

	messages, err := s.ChannelMessages(channelID, 100, "", "", "")
	if err != nil {
		return result, err
	}
	result.MessagesChecked = len(messages)

	for _, msg := range messages {
		for _, reaction := range msg.Reactions {
			if reaction == nil || reaction.Emoji == nil {
				continue
			}

			reason, prohibited := prohibitedReactionReason(reaction.Emoji.Name)
			if !prohibited {
				continue
			}

			afterID := ""
			for {
				users, err := s.MessageReactions(channelID, msg.ID, reaction.Emoji.APIName(), 100, "", afterID)
				if err != nil {
					result.FetchFailures++
					b.Logger.Error("failed to fetch prohibited reaction users",
						zap.Error(err),
						zap.String("messageID", msg.ID),
						zap.String("emoji", reaction.Emoji.Name))
					break
				}
				if len(users) == 0 {
					break
				}

				for _, user := range users {
					if user == nil || user.ID == "" || isSessionUser(s, user.ID) {
						continue
					}

					result.ReactionsFound++
					removed, warned, skipped := b.removeAndWarnProhibitedReaction(s, channelID, msg.ID, user.ID, reaction.Emoji.APIName(), reason)
					if removed {
						result.ReactionsRemoved++
					} else {
						result.RemoveFailures++
					}
					if warned {
						result.WarningsIssued++
					} else if skipped {
						result.WarningsSkipped++
					} else {
						result.WarnFailures++
					}
				}

				if len(users) < 100 {
					break
				}
				afterID = users[len(users)-1].ID
			}
		}
	}

	return result, nil
}

func (b *Bot) ignoreBoardChannel(s *discordgo.Session, channelID string) bool {
	ignored, err := boardChannelIgnored(s, channelID)
	if err != nil {
		b.Logger.Warn("board reaction ignored - failed to resolve channel category",
			zap.Error(err), zap.String("channelID", channelID))
		return true
	}
	return ignored
}

func boardChannelIgnored(s *discordgo.Session, channelID string) (bool, error) {
	channel, err := getDiscordChannel(s, channelID)
	if err != nil {
		return false, err
	}
	categoryID := channel.ParentID
	if channel.IsThread() {
		parent, err := getDiscordChannel(s, channel.ParentID)
		if err != nil {
			return false, err
		}
		categoryID = parent.ParentID
	}
	_, ignored := ignoredBoardCategories[categoryID]
	return ignored, nil
}

func getDiscordChannel(s *discordgo.Session, channelID string) (*discordgo.Channel, error) {
	if s.State != nil {
		if channel, err := s.State.Channel(channelID); err == nil {
			return channel, nil
		}
	}
	return s.Channel(channelID)
}

func (b *Bot) handleBoardMessageReaction(s *discordgo.Session, boardMessageID string, board reactionBoard) {
	entry, err := b.DB.GetBoardEntryByStarboardMsgID(board.name, boardMessageID)
	if err != nil {
		b.Logger.Error("failed to find board entry", zap.Error(err), zap.String("board", board.name))
		return
	}
	if entry == nil || b.ignoreBoardChannel(s, entry.ChannelID) {
		return
	}

	msg, err := s.ChannelMessage(entry.ChannelID, entry.OriginalMsgID)
	if err != nil {
		b.Logger.Error("failed to get original board message", zap.Error(err), zap.String("board", board.name))
		return
	}
	count := b.countTotalBoardReactions(s, msg, entry.StarboardMsgID, board)
	b.syncBoardEntry(s, board, entry, msg, count)
}

func (b *Bot) handleOriginalMessageReaction(s *discordgo.Session, channelID, messageID, guildID string, board reactionBoard) {
	msg, err := s.ChannelMessage(channelID, messageID)
	if err != nil {
		b.Logger.Error("failed to get message for board", zap.Error(err), zap.String("board", board.name))
		return
	}
	if msg.Author == nil {
		return
	}

	entry, err := b.DB.GetBoardEntry(board.name, messageID)
	if err != nil {
		b.Logger.Error("failed to get board entry", zap.Error(err), zap.String("board", board.name))
		return
	}
	var boardMessageID *string
	if entry != nil {
		boardMessageID = entry.StarboardMsgID
	}
	count := b.countTotalBoardReactions(s, msg, boardMessageID, board)

	if entry != nil {
		b.syncBoardEntry(s, board, entry, msg, count)
		return
	}
	if count < b.boardThreshold() {
		return
	}

	entry, err = b.DB.AddBoardEntry(
		board.name,
		messageID,
		channelID,
		guildID,
		msg.Author.ID,
		msg.Content,
		count,
		time.Now().Unix(),
	)
	if err != nil {
		b.Logger.Error("failed to add board entry", zap.Error(err), zap.String("board", board.name))
		return
	}

	postedID, err := b.postToBoard(s, msg, count, board)
	if err != nil {
		b.DB.DeleteBoardEntry(board.name, messageID)
		b.Logger.Error("failed to post board entry", zap.Error(err), zap.String("board", board.name))
		return
	}
	if err := b.DB.UpdateBoardEntry(board.name, messageID, count, &postedID); err != nil {
		b.Logger.Error("failed to save board message ID", zap.Error(err), zap.String("board", board.name))
	}
}

// handleReactionRemove handles reaction remove events for starboard and sobboard.
func (b *Bot) handleReactionRemove(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
	if b.Config.StarboardChannelID == "" || r.GuildID != b.Config.DiscordGuildID {
		return
	}
	board, ok := b.reactionBoard(r.Emoji.Name)
	if !ok {
		return
	}
	if r.ChannelID == b.Config.StarboardChannelID {
		b.handleBoardMessageReaction(s, r.MessageID, board)
		return
	}
	if b.ignoreBoardChannel(s, r.ChannelID) {
		return
	}
	b.handleOriginalMessageReactionRemove(s, r.ChannelID, r.MessageID, board)
}

func (b *Bot) handleOriginalMessageReactionRemove(s *discordgo.Session, channelID, messageID string, board reactionBoard) {
	entry, err := b.DB.GetBoardEntry(board.name, messageID)
	if err != nil || entry == nil {
		return
	}

	msg, err := s.ChannelMessage(channelID, messageID)
	if err != nil {
		count := entry.StarCount - 1
		if count < 0 {
			count = 0
		}
		b.syncBoardEntry(s, board, entry, nil, count)
		return
	}
	count := b.countTotalBoardReactions(s, msg, entry.StarboardMsgID, board)
	b.syncBoardEntry(s, board, entry, msg, count)
}

func (b *Bot) syncBoardEntry(s *discordgo.Session, board reactionBoard, entry *db.BoardEntry, msg *discordgo.Message, count int) {
	if count < b.boardThreshold() {
		if entry.StarboardMsgID != nil {
			if err := s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID); err != nil {
				b.Logger.Warn("failed to delete board message", zap.Error(err), zap.String("board", board.name))
			}
		}
		if err := b.DB.DeleteBoardEntry(board.name, entry.OriginalMsgID); err != nil {
			b.Logger.Error("failed to delete board entry", zap.Error(err), zap.String("board", board.name))
		}
		return
	}

	if err := b.DB.UpdateBoardEntry(board.name, entry.OriginalMsgID, count, entry.StarboardMsgID); err != nil {
		b.Logger.Error("failed to update board entry", zap.Error(err), zap.String("board", board.name))
		return
	}
	if entry.StarboardMsgID != nil {
		b.updateBoardMessage(s, board, *entry.StarboardMsgID, msg, count)
	}
}

// handleMessageDelete cleans both boards when an original message is deleted.
func (b *Bot) handleMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	if b.Config.StarboardChannelID == "" {
		return
	}
	for _, board := range b.reactionBoards() {
		entry, err := b.DB.GetBoardEntry(board.name, m.ID)
		if err != nil || entry == nil {
			continue
		}
		if entry.StarboardMsgID != nil {
			s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID)
		}
		b.DB.DeleteBoardEntry(board.name, m.ID)
	}
}

func countReactions(reactions []*discordgo.MessageReactions, emoji string) int {
	for _, reaction := range reactions {
		if reaction == nil || reaction.Emoji == nil {
			continue
		}
		if reaction.Emoji.Name == emoji || reaction.Emoji.ID != "" && reaction.Emoji.ID == emoji {
			return reaction.Count
		}
	}
	return 0
}

func (b *Bot) countTotalBoardReactions(s *discordgo.Session, originalMsg *discordgo.Message, boardMessageID *string, board reactionBoard) int {
	count := 0
	if originalMsg != nil {
		count = countReactions(originalMsg.Reactions, board.emoji)
	}
	if boardMessageID == nil {
		return count
	}

	boardMessage, err := s.ChannelMessage(b.Config.StarboardChannelID, *boardMessageID)
	if err != nil {
		b.Logger.Warn("failed to get board message for reaction count", zap.Error(err), zap.String("board", board.name))
		return count
	}
	return count + countReactions(boardMessage.Reactions, board.emoji)
}

func (b *Bot) postToBoard(s *discordgo.Session, msg *discordgo.Message, count int, board reactionBoard) (string, error) {
	guildID := msg.GuildID
	if guildID == "" {
		guildID = b.Config.DiscordGuildID
	}
	embed := &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			Name:    msg.Author.Username,
			IconURL: msg.Author.AvatarURL(""),
		},
		Description: boardDescription(msg, guildID),
		Color:       0xFFD700,
		Timestamp:   msg.Timestamp.Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("#%s", msg.ChannelID),
		},
	}
	for _, attachment := range msg.Attachments {
		if isImage(attachment.ContentType) {
			embed.Image = &discordgo.MessageEmbedImage{URL: attachment.URL}
			break
		}
	}

	messageURL := discordMessageURL(guildID, msg.ChannelID, msg.ID)
	posted, err := s.ChannelMessageSendComplex(b.Config.StarboardChannelID, &discordgo.MessageSend{
		Content: fmt.Sprintf("%s %d | <%s>", board.emoji, count, messageURL),
		Embeds:  []*discordgo.MessageEmbed{embed},
	})
	if err != nil {
		return "", err
	}
	return posted.ID, nil
}

func boardDescription(msg *discordgo.Message, guildID string) string {
	description := msg.Content
	if description == "" && len(msg.Embeds) > 0 {
		description = "*[Message contains embeds]*"
	}
	if msg.MessageReference == nil {
		return truncateRunes(description, 4096)
	}

	replyText := "*[Replied message unavailable]*"
	replyAuthor := "a message"
	channelID := msg.ChannelID
	if msg.MessageReference.ChannelID != "" {
		channelID = msg.MessageReference.ChannelID
	}
	messageID := msg.MessageReference.MessageID
	if reply := msg.ReferencedMessage; reply != nil {
		if reply.Content != "" {
			replyText = reply.Content
		} else if len(reply.Attachments) > 0 || len(reply.Embeds) > 0 {
			replyText = "*[Message contains attachments or embeds]*"
		}
		if reply.Author != nil {
			replyAuthor = "@" + reply.Author.Username
		}
		if reply.ChannelID != "" {
			channelID = reply.ChannelID
		}
		if reply.ID != "" {
			messageID = reply.ID
		}
	}

	header := fmt.Sprintf("-# Replying to [%s](%s)\n", replyAuthor, discordMessageURL(guildID, channelID, messageID))
	replyBlock := header + "> " + strings.ReplaceAll(replyText, "\n", "\n> ")
	description = truncateRunes(description, 3840)
	available := 4096 - len([]rune(description)) - 2
	return truncateRunes(replyBlock, available) + "\n\n" + description
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func discordMessageURL(guildID, channelID, messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}

func (b *Bot) updateBoardMessage(s *discordgo.Session, board reactionBoard, boardMessageID string, msg *discordgo.Message, count int) {
	var messageURL string
	if msg != nil {
		guildID := msg.GuildID
		if guildID == "" {
			guildID = b.Config.DiscordGuildID
		}
		messageURL = discordMessageURL(guildID, msg.ChannelID, msg.ID)
	} else if entry, err := b.DB.GetBoardEntryByStarboardMsgID(board.name, boardMessageID); err == nil && entry != nil {
		messageURL = discordMessageURL(entry.GuildID, entry.ChannelID, entry.OriginalMsgID)
	}

	content := fmt.Sprintf("%s %d", board.emoji, count)
	if messageURL != "" {
		content += fmt.Sprintf(" | <%s>", messageURL)
	}
	if _, err := s.ChannelMessageEdit(b.Config.StarboardChannelID, boardMessageID, content); err != nil {
		b.Logger.Warn("failed to update board message", zap.Error(err), zap.String("board", board.name))
	}
}

// RefreshAllStarboard refreshes both starboard and sobboard entries.
func (b *Bot) RefreshAllStarboard(s *discordgo.Session) error {
	for _, board := range b.reactionBoards() {
		entries, err := b.DB.GetAllBoardEntries(board.name)
		if err != nil {
			return fmt.Errorf("getting %s entries: %w", board.name, err)
		}
		for _, entry := range entries {
			ignored, err := boardChannelIgnored(s, entry.ChannelID)
			if err != nil {
				b.Logger.Warn("failed to resolve board source category", zap.Error(err), zap.String("board", board.name))
				continue
			}
			if ignored {
				if entry.StarboardMsgID != nil {
					s.ChannelMessageDelete(b.Config.StarboardChannelID, *entry.StarboardMsgID)
				}
				b.DB.DeleteBoardEntry(board.name, entry.OriginalMsgID)
				continue
			}

			msg, err := s.ChannelMessage(entry.ChannelID, entry.OriginalMsgID)
			if err != nil {
				b.Logger.Error("failed to get message during board refresh", zap.Error(err), zap.String("board", board.name))
				continue
			}
			count := b.countTotalBoardReactions(s, msg, entry.StarboardMsgID, board)
			b.syncBoardEntry(s, board, entry, msg, count)
		}
	}
	return nil
}

func isImage(contentType string) bool {
	return strings.HasPrefix(contentType, "image/")
}
