package discord

import (
	"fmt"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
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

func isSessionUser(s *bot.Client, userID string) bool {
	return s.Caches != nil && s.ID() != 0 && userID == s.ID().String()
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
func (b *Bot) handleReactionAdd(s *bot.Client, r *events.MessageReactionAdd) {
	if b.handleProhibitedReaction(s, r) {
		return
	}
	if b.Config.StarboardChannelID == "" || snowflakeString(r.GuildID) != b.Config.DiscordGuildID {
		return
	}

	board, ok := b.reactionBoard(value(r.Emoji.Name))
	if !ok {
		return
	}
	if r.ChannelID.String() == b.Config.StarboardChannelID {
		b.handleBoardMessageReaction(s, r.MessageID.String(), board)
		return
	}
	if b.ignoreBoardChannel(s, r.ChannelID.String()) {
		return
	}
	b.handleOriginalMessageReaction(s, r.ChannelID.String(), r.MessageID.String(), snowflakeString(r.GuildID), board)
}

func (b *Bot) handleProhibitedReaction(s *bot.Client, r *events.MessageReactionAdd) bool {
	reason, prohibited := prohibitedReactionReason(value(r.Emoji.Name))
	if snowflakeString(r.GuildID) != b.Config.DiscordGuildID || r.UserID == 0 || !prohibited {
		return false
	}
	if isSessionUser(s, r.UserID.String()) {
		return true
	}

	b.removeAndWarnProhibitedReaction(s, r.ChannelID.String(), r.MessageID.String(), r.UserID.String(), r.Emoji.Reaction(), reason)
	return true
}

func (b *Bot) removeAndWarnProhibitedReaction(s *bot.Client, channelID, messageID, userID, emojiAPIName, reason string) (bool, bool, bool) {
	removed := true
	if err := s.Rest.RemoveUserReaction(snowflakeID(channelID), snowflakeID(messageID), emojiAPIName, snowflakeID(userID)); err != nil {
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

func (b *Bot) scanProhibitedReactions(s *bot.Client, channelID string) (prohibitedReactionScanResult, error) {
	var result prohibitedReactionScanResult

	messages, err := s.Rest.GetMessages(snowflakeID(channelID), 0, 0, 0, 100)
	if err != nil {
		return result, err
	}
	result.MessagesChecked = len(messages)

	for _, msg := range messages {
		for _, reaction := range msg.Reactions {
			if reaction.Emoji.Name == "" && reaction.Emoji.ID == 0 {
				continue
			}

			reason, prohibited := prohibitedReactionReason(reaction.Emoji.Name)
			if !prohibited {
				continue
			}

			emojiAPIName := reaction.Emoji.Name
			if reaction.Emoji.ID != 0 {
				emojiAPIName = reaction.Emoji.Reaction()
			}
			afterID := ""
			for {
				users, err := s.Rest.GetReactions(snowflakeID(channelID), msg.ID, emojiAPIName, discord.MessageReactionTypeNormal, int(snowflakeID(afterID)), 100)
				if err != nil {
					result.FetchFailures++
					b.Logger.Error("failed to fetch prohibited reaction users",
						zap.Error(err),
						zap.String("messageID", msg.ID.String()),
						zap.String("emoji", reaction.Emoji.Name))
					break
				}
				if len(users) == 0 {
					break
				}

				for _, user := range users {
					if user.ID == 0 || isSessionUser(s, user.ID.String()) {
						continue
					}

					result.ReactionsFound++
					removed, warned, skipped := b.removeAndWarnProhibitedReaction(s, channelID, msg.ID.String(), user.ID.String(), emojiAPIName, reason)
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
				afterID = users[len(users)-1].ID.String()
			}
		}
	}

	return result, nil
}

func (b *Bot) ignoreBoardChannel(s *bot.Client, channelID string) bool {
	ignored, err := boardChannelIgnored(s, channelID)
	if err != nil {
		b.Logger.Warn("board reaction ignored - failed to resolve channel category",
			zap.Error(err), zap.String("channelID", channelID))
		return true
	}
	return ignored
}

func boardChannelIgnored(s *bot.Client, channelID string) (bool, error) {
	channel, err := getDiscordChannel(s, channelID)
	if err != nil {
		return false, err
	}
	categoryID := snowflakeString(channel.ParentID())
	if isThread(channel) {
		parent, err := getDiscordChannel(s, snowflakeString(channel.ParentID()))
		if err != nil {
			return false, err
		}
		categoryID = snowflakeString(parent.ParentID())
	}
	_, ignored := ignoredBoardCategories[categoryID]
	return ignored, nil
}

func getDiscordChannel(s *bot.Client, channelID string) (discord.GuildChannel, error) {
	if s == nil {
		return nil, fmt.Errorf("Discord client is unavailable")
	}
	if s.Caches != nil {
		if channel, ok := s.Caches.Channel(snowflakeID(channelID)); ok {
			return channel, nil
		}
	}
	if s.Rest == nil {
		return nil, fmt.Errorf("Discord channel is unavailable")
	}
	channel, err := s.Rest.GetChannel(snowflakeID(channelID))
	if err != nil {
		return nil, err
	}
	guildChannel, ok := channel.(discord.GuildChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s is not a guild channel", channelID)
	}
	return guildChannel, nil
}

func (b *Bot) handleBoardMessageReaction(s *bot.Client, boardMessageID string, board reactionBoard) {
	entry, err := b.DB.GetBoardEntryByStarboardMsgID(board.name, boardMessageID)
	if err != nil {
		b.Logger.Error("failed to find board entry", zap.Error(err), zap.String("board", board.name))
		return
	}
	if entry == nil || b.ignoreBoardChannel(s, entry.ChannelID) {
		return
	}

	msg, err := s.Rest.GetMessage(snowflakeID(entry.ChannelID), snowflakeID(entry.OriginalMsgID))
	if err != nil {
		b.Logger.Error("failed to get original board message", zap.Error(err), zap.String("board", board.name))
		return
	}
	count := b.countTotalBoardReactions(s, msg, entry.StarboardMsgID, board)
	b.syncBoardEntry(s, board, entry, msg, count)
}

func (b *Bot) handleOriginalMessageReaction(s *bot.Client, channelID, messageID, guildID string, board reactionBoard) {
	msg, err := s.Rest.GetMessage(snowflakeID(channelID), snowflakeID(messageID))
	if err != nil {
		b.Logger.Error("failed to get message for board", zap.Error(err), zap.String("board", board.name))
		return
	}
	if msg.Author.ID == 0 {
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
		msg.Author.ID.String(),
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
func (b *Bot) handleReactionRemove(s *bot.Client, r *events.MessageReactionRemove) {
	if b.Config.StarboardChannelID == "" || snowflakeString(r.GuildID) != b.Config.DiscordGuildID {
		return
	}
	board, ok := b.reactionBoard(value(r.Emoji.Name))
	if !ok {
		return
	}
	if r.ChannelID.String() == b.Config.StarboardChannelID {
		b.handleBoardMessageReaction(s, r.MessageID.String(), board)
		return
	}
	if b.ignoreBoardChannel(s, r.ChannelID.String()) {
		return
	}
	b.handleOriginalMessageReactionRemove(s, r.ChannelID.String(), r.MessageID.String(), board)
}

func (b *Bot) handleOriginalMessageReactionRemove(s *bot.Client, channelID, messageID string, board reactionBoard) {
	entry, err := b.DB.GetBoardEntry(board.name, messageID)
	if err != nil || entry == nil {
		return
	}

	msg, err := s.Rest.GetMessage(snowflakeID(channelID), snowflakeID(messageID))
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

func (b *Bot) syncBoardEntry(s *bot.Client, board reactionBoard, entry *db.BoardEntry, msg *discord.Message, count int) {
	if count < b.boardThreshold() {
		if entry.StarboardMsgID != nil {
			if err := s.Rest.DeleteMessage(snowflakeID(b.Config.StarboardChannelID), snowflakeID(*entry.StarboardMsgID)); err != nil {
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
func (b *Bot) handleMessageDelete(s *bot.Client, m *events.MessageDelete) {
	if b.Config.StarboardChannelID == "" {
		return
	}
	for _, board := range b.reactionBoards() {
		entry, err := b.DB.GetBoardEntry(board.name, m.MessageID.String())
		if err != nil || entry == nil {
			continue
		}
		if entry.StarboardMsgID != nil {
			s.Rest.DeleteMessage(snowflakeID(b.Config.StarboardChannelID), snowflakeID(*entry.StarboardMsgID))
		}
		b.DB.DeleteBoardEntry(board.name, m.MessageID.String())
	}
}

func countReactions(reactions []discord.MessageReaction, emoji string) int {
	for _, reaction := range reactions {
		if reaction.Emoji.Name == "" && reaction.Emoji.ID == 0 {
			continue
		}
		if reaction.Emoji.Name == emoji || reaction.Emoji.ID != 0 && reaction.Emoji.ID.String() == emoji {
			return reaction.Count
		}
	}
	return 0
}

func (b *Bot) countTotalBoardReactions(s *bot.Client, originalMsg *discord.Message, boardMessageID *string, board reactionBoard) int {
	count := 0
	if originalMsg != nil {
		count = countReactions(originalMsg.Reactions, board.emoji)
	}
	if boardMessageID == nil {
		return count
	}

	boardMessage, err := s.Rest.GetMessage(snowflakeID(b.Config.StarboardChannelID), snowflakeID(*boardMessageID))
	if err != nil {
		b.Logger.Warn("failed to get board message for reaction count", zap.Error(err), zap.String("board", board.name))
		return count
	}
	return count + countReactions(boardMessage.Reactions, board.emoji)
}

func (b *Bot) postToBoard(s *bot.Client, msg *discord.Message, count int, board reactionBoard) (string, error) {
	guildID := snowflakeString(msg.GuildID)
	if guildID == "" {
		guildID = b.Config.DiscordGuildID
	}
	embed := &discord.Embed{
		Author: &discord.EmbedAuthor{
			Name:    msg.Author.Username,
			IconURL: msg.Author.EffectiveAvatarURL(),
		},
		Description: boardDescription(msg, guildID),
		Color:       0xFFD700,
		Timestamp:   ptr(msg.CreatedAt),
		Footer: &discord.EmbedFooter{
			Text: fmt.Sprintf("#%s", msg.ChannelID.String()),
		},
	}
	for _, attachment := range msg.Attachments {
		if isImage(value(attachment.ContentType)) {
			embed.Image = &discord.EmbedResource{URL: attachment.URL}
			break
		}
	}

	messageURL := discordMessageURL(guildID, msg.ChannelID.String(), msg.ID.String())
	posted, err := s.Rest.CreateMessage(snowflakeID(b.Config.StarboardChannelID), discord.MessageCreate{
		Content: fmt.Sprintf("%s %d | <%s>", board.emoji, count, messageURL),
		Embeds:  []discord.Embed{*embed},
	})
	if err != nil {
		return "", err
	}
	return posted.ID.String(), nil
}

func boardDescription(msg *discord.Message, guildID string) string {
	description := msg.Content
	if description == "" && len(msg.Embeds) > 0 {
		description = "*[Message contains embeds]*"
	}
	if msg.MessageReference == nil {
		return truncateRunes(description, 4096)
	}

	replyText := "*[Replied message unavailable]*"
	replyAuthor := "a message"
	channelID := msg.ChannelID.String()
	if snowflakeString(msg.MessageReference.ChannelID) != "" {
		channelID = snowflakeString(msg.MessageReference.ChannelID)
	}
	messageID := snowflakeString(msg.MessageReference.MessageID)
	if reply := msg.ReferencedMessage; reply != nil {
		if reply.Content != "" {
			replyText = reply.Content
		} else if len(reply.Attachments) > 0 || len(reply.Embeds) > 0 {
			replyText = "*[Message contains attachments or embeds]*"
		}
		if reply.Author.ID != 0 {
			replyAuthor = "@" + reply.Author.Username
		}
		if reply.ChannelID != 0 {
			channelID = reply.ChannelID.String()
		}
		if reply.ID != 0 {
			messageID = reply.ID.String()
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

func (b *Bot) updateBoardMessage(s *bot.Client, board reactionBoard, boardMessageID string, msg *discord.Message, count int) {
	var messageURL string
	if msg != nil {
		guildID := snowflakeString(msg.GuildID)
		if guildID == "" {
			guildID = b.Config.DiscordGuildID
		}
		messageURL = discordMessageURL(guildID, msg.ChannelID.String(), msg.ID.String())
	} else if entry, err := b.DB.GetBoardEntryByStarboardMsgID(board.name, boardMessageID); err == nil && entry != nil {
		messageURL = discordMessageURL(entry.GuildID, entry.ChannelID, entry.OriginalMsgID)
	}

	content := fmt.Sprintf("%s %d", board.emoji, count)
	if messageURL != "" {
		content += fmt.Sprintf(" | <%s>", messageURL)
	}
	if _, err := s.Rest.UpdateMessage(snowflakeID(b.Config.StarboardChannelID), snowflakeID(boardMessageID), discord.MessageUpdate{Content: ptr(content)}); err != nil {
		b.Logger.Warn("failed to update board message", zap.Error(err), zap.String("board", board.name))
	}
}

// RefreshAllStarboard refreshes both starboard and sobboard entries.
func (b *Bot) RefreshAllStarboard(s *bot.Client) error {
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
					s.Rest.DeleteMessage(snowflakeID(b.Config.StarboardChannelID), snowflakeID(*entry.StarboardMsgID))
				}
				b.DB.DeleteBoardEntry(board.name, entry.OriginalMsgID)
				continue
			}

			msg, err := s.Rest.GetMessage(snowflakeID(entry.ChannelID), snowflakeID(entry.OriginalMsgID))
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
