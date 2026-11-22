package telegram

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"github.com/MetrolistGroup/metrobot/internal/notefmt"
	"github.com/MetrolistGroup/metrobot/util"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Telegram callback data is capped at 64 bytes, so names are hashed.
func faqCallbackHash(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return fmt.Sprintf("%x", sum[:8])
}

func faqCallbackData(name string, index int) string {
	return fmt.Sprintf("faq:%s:%d", faqCallbackHash(name), index)
}

func faqButtonLabel(question string, selected bool) string {
	label := strings.NewReplacer("*", "", "`", "").Replace(question)
	if runes := []rune(label); len(runes) > 64 {
		label = string(runes[:63]) + "…"
	}
	if selected {
		return "• " + label
	}
	return label
}

// faqKeyboard lists every question so the reader can switch answers, as Discord's selector does.
func faqKeyboard(name string, items []notefmt.Question, selected int) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, len(items))
	for index, item := range items {
		button := tgbotapi.NewInlineKeyboardButtonData(faqButtonLabel(item.Question, index == selected), faqCallbackData(name, index))
		rows[index] = tgbotapi.NewInlineKeyboardRow(button)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// linkKeyboard maps Components V2 button rows to Telegram URL buttons.
func linkKeyboard(layout *notefmt.Layout) (tgbotapi.InlineKeyboardMarkup, bool) {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, c := range layout.Components {
		if c.Type != "buttons" || len(c.Buttons) == 0 {
			continue
		}
		row := make([]tgbotapi.InlineKeyboardButton, len(c.Buttons))
		for i, button := range c.Buttons {
			row[i] = tgbotapi.NewInlineKeyboardButtonURL(button.Label, button.URL)
		}
		rows = append(rows, row)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...), len(rows) > 0
}

// faqPageText is the readable page for one selected question, including surrounding text.
func faqPageText(note notefmt.Rendered, item notefmt.Question) string {
	return note.Before + note.FAQ.DisplayHeader() + "\n\n## " + item.Question + "\n" + item.Answer + note.After
}

// newTelegramNoteKeyboardMessage returns false for plain notes and for text too long for one message.
func newTelegramNoteKeyboardMessage(chatID int64, name string, note notefmt.Rendered) (tgbotapi.MessageConfig, bool) {
	var text string
	var markup tgbotapi.InlineKeyboardMarkup
	switch {
	case note.FAQ != nil:
		text = note.Before + note.FAQ.DisplayHeader() + note.After
		markup = faqKeyboard(name, note.FAQ.Items, -1)
	case note.Layout != nil:
		var ok bool
		if markup, ok = linkKeyboard(note.Layout); !ok {
			return tgbotapi.MessageConfig{}, false
		}
		text = note.Text
	default:
		return tgbotapi.MessageConfig{}, false
	}
	msg := newTelegramNoteMessage(chatID, text)
	if util.UTF16Len(msg.Text) > 4096 {
		return tgbotapi.MessageConfig{}, false
	}
	msg.ReplyMarkup = markup
	return msg, true
}

// sendNote replies with the note. Interactive notes use inline keyboards; plain notes keep the text path.
func (b *Bot) sendNote(chatID int64, replyToMsgID int, name string, autoDelete bool) error {
	document, err := b.Notes.GetNoteDocument(name)
	if err != nil {
		return err
	}
	note := document.Render()
	msg, ok := newTelegramNoteKeyboardMessage(chatID, name, note)
	if !ok {
		sendPublicNoteReply(b.API, chatID, replyToMsgID, note.Text, autoDelete, b.Logger)
		return nil
	}
	msg.ReplyToMessageID = replyToMsgID
	sent, err := b.API.Send(msg)
	if err != nil {
		b.Logger.Error("failed to send telegram note", zap.Error(err))
		return nil
	}
	if autoDelete {
		scheduleDelete(b.API, chatID, sent.MessageID, b.Logger)
		scheduleDelete(b.API, chatID, replyToMsgID, b.Logger)
	}
	return nil
}

func (b *Bot) handleNoteCallback(q *tgbotapi.CallbackQuery) {
	answer := func(callback tgbotapi.CallbackConfig) {
		if _, err := b.API.Request(callback); err != nil {
			b.Logger.Debug("failed to answer telegram callback", zap.Error(err))
		}
	}
	unavailable := func() { answer(tgbotapi.NewCallbackWithAlert(q.ID, "That FAQ question is unavailable.")) }
	parts := strings.Split(q.Data, ":")
	if len(parts) != 3 || parts[0] != "faq" || q.Message == nil || q.Message.Chat.ID != b.Config.TelegramChatID {
		unavailable()
		return
	}
	selected, err := strconv.Atoi(parts[2])
	if err != nil {
		unavailable()
		return
	}
	names, err := b.Notes.DB.ListNotes()
	if err != nil {
		unavailable()
		return
	}
	name := ""
	for _, n := range names {
		if faqCallbackHash(n.Name) == parts[1] {
			name = n.Name
			break
		}
	}
	document, err := b.Notes.GetNoteDocument(name)
	if err != nil || name == "" {
		unavailable()
		return
	}
	note := document.Render()
	if note.FAQ == nil || selected < 0 || selected >= len(note.FAQ.Items) {
		unavailable()
		return
	}
	item := note.FAQ.Items[selected]
	page := faqPageText(note, item)
	chatID, messageID := q.Message.Chat.ID, q.Message.MessageID
	markup := faqKeyboard(name, note.FAQ.Items, selected)
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, formatTelegramNoteHTML(page), markup)
	edit.ParseMode = "HTML"
	edit.DisableWebPagePreview = !hasEmbeddableDiscordURL(page)
	if util.UTF16Len(edit.Text) > 4096 {
		// An edit cannot grow into a file, so send the page as a reply and leave the menu intact.
		file := tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{Name: "note.md", Bytes: []byte(page)})
		file.ReplyToMessageID = messageID
		if _, err := b.API.Send(file); err != nil {
			b.Logger.Error("failed to send telegram FAQ page", zap.Error(err))
		}
		answer(tgbotapi.NewCallback(q.ID, ""))
		return
	}
	if _, err := b.API.Request(edit); err != nil {
		b.Logger.Debug("failed to edit telegram FAQ page", zap.Error(err))
	}
	answer(tgbotapi.NewCallback(q.ID, ""))
}
