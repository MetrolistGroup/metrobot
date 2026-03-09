package telegram

import (
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

func dmUser(bot *tgbotapi.BotAPI, userID int64, text string) error {
	msg := tgbotapi.NewMessage(userID, text)
	msg.DisableWebPagePreview = true
	_, err := bot.Send(msg)
	return err
}
