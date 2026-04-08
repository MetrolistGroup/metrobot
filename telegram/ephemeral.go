package telegram

import (
	"fmt"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

func sendEphemeralReply(bot *tgbotapi.BotAPI, chatID int64, replyToMsgID int, text string, parseMode string, stay bool, logger *zap.Logger) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyToMsgID
	msg.DisableWebPagePreview = true
	if parseMode != "" {
		msg.ParseMode = parseMode
	}

	sent, err := bot.Send(msg)
	if err != nil {
		logger.Error("failed to send telegram message", zap.Error(err))
		return
	}

	if !stay {
		scheduleDelete(bot, chatID, sent.MessageID, logger)
		scheduleDelete(bot, chatID, replyToMsgID, logger)
	}
}

func sendPublicReply(bot *tgbotapi.BotAPI, chatID int64, replyToMsgID int, text string, parseMode string, autoDelete bool, logger *zap.Logger) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyToMsgID
	msg.DisableWebPagePreview = true
	if parseMode != "" {
		msg.ParseMode = parseMode
	}

	sent, err := bot.Send(msg)
	if err != nil {
		logger.Error("failed to send telegram message", zap.Error(err))
		return
	}

	if autoDelete {
		scheduleDelete(bot, chatID, sent.MessageID, logger)
		scheduleDelete(bot, chatID, replyToMsgID, logger)
	}
}

func scheduleDelete(bot *tgbotapi.BotAPI, chatID int64, msgID int, logger *zap.Logger) {
	time.AfterFunc(15*time.Minute, func() {
		del := tgbotapi.NewDeleteMessage(chatID, msgID)
		if _, err := bot.Request(del); err != nil {
			logger.Debug("failed to delete telegram message", zap.Int("msg_id", msgID), zap.Error(err))
		}
	})
}

// scheduleDeleteAfter deletes a message after the specified duration
func scheduleDeleteAfter(bot *tgbotapi.BotAPI, chatID int64, msgID int, duration time.Duration, logger *zap.Logger) {
	time.AfterFunc(duration, func() {
		del := tgbotapi.NewDeleteMessage(chatID, msgID)
		if _, err := bot.Request(del); err != nil {
			logger.Debug("failed to delete telegram message", zap.Int("msg_id", msgID), zap.Error(err))
		}
	})
}

func dmUser(bot *tgbotapi.BotAPI, userID int64, text string) error {
	msg := tgbotapi.NewMessage(userID, text)
	msg.DisableWebPagePreview = true
	_, err := bot.Send(msg)
	return err
}

// sendPermissionError sends an error message about missing permissions and deletes both messages after 5 seconds
func sendPermissionError(bot *tgbotapi.BotAPI, chatID int64, originalMsgID int, permission string, logger *zap.Logger) {
	text := fmt.Sprintf("❌ I don't have the required permission: %s", permission)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = originalMsgID

	sent, err := bot.Send(msg)
	if err != nil {
		logger.Error("failed to send permission error", zap.Error(err))
		return
	}

	// Delete both messages after 5 seconds
	time.AfterFunc(5*time.Second, func() {
		del := tgbotapi.NewDeleteMessage(chatID, sent.MessageID)
		bot.Request(del)
		if originalMsgID != 0 {
			delOrig := tgbotapi.NewDeleteMessage(chatID, originalMsgID)
			bot.Request(delOrig)
		}
	})
}
