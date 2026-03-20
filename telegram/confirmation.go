package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/util"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// pendingConfirmation stores information about a pending command confirmation
type pendingConfirmation struct {
	action     string
	args       string
	callerID   string
	targetID   string
	chatID     int64
	messageID  int
	timestamp  time.Time
	cancelFunc context.CancelFunc
}

// confirmationStore stores pending confirmations by message ID
type confirmationStore struct {
	confirmations map[int]*pendingConfirmation
}

func newConfirmationStore() *confirmationStore {
	return &confirmationStore{
		confirmations: make(map[int]*pendingConfirmation),
	}
}

func (cs *confirmationStore) add(id int, pc *pendingConfirmation) {
	cs.confirmations[id] = pc
}

func (cs *confirmationStore) get(id int) (*pendingConfirmation, bool) {
	pc, ok := cs.confirmations[id]
	return pc, ok
}

func (cs *confirmationStore) remove(id int) {
	delete(cs.confirmations, id)
}

// sendConfirmation sends a confirmation message with Yes/No inline keyboard
func (b *Bot) sendConfirmation(chatID int64, callerID, action, args, targetID string) (int, error) {
	actionUpper := strings.ToUpper(action)
	targetRef := ""
	if targetID != "" {
		targetRef = fmt.Sprintf(" %s", targetID)
	}

	text := fmt.Sprintf("*Confirm %s*\n\nAre you sure you want to %s%s?\n\n*Args:* %s", actionUpper, action, targetRef, args)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes", "confirm_yes"),
			tgbotapi.NewInlineKeyboardButtonData("❌ No", "confirm_no"),
		),
	)
	msg.ReplyMarkup = keyboard

	sent, err := b.API.Send(msg)
	if err != nil {
		return 0, err
	}

	return sent.MessageID, nil
}

// handleConfirmationCallback handles callback queries for confirmations
func (b *Bot) handleConfirmationCallback(query *tgbotapi.CallbackQuery) {
	if query.Data != "confirm_yes" && query.Data != "confirm_no" {
		return
	}

	pc, ok := b.confirmations.get(query.Message.MessageID)
	if !ok {
		// Answer the callback query
		callback := tgbotapi.NewCallback(query.ID, "This confirmation has expired.")
		b.API.Request(callback)
		return
	}

	// Verify the user clicking is the original caller
	callerID := strconv.FormatInt(query.From.ID, 10)
	if callerID != pc.callerID {
		callback := tgbotapi.NewCallback(query.ID, "Only the command issuer can confirm.")
		b.API.Request(callback)
		return
	}

	// Cancel the timeout
	if pc.cancelFunc != nil {
		pc.cancelFunc()
	}
	b.confirmations.remove(query.Message.MessageID)

	// Answer the callback query
	callback := tgbotapi.NewCallback(query.ID, "")
	b.API.Request(callback)

	if query.Data == "confirm_no" {
		// Delete the confirmation message
		del := tgbotapi.NewDeleteMessage(pc.chatID, pc.messageID)
		b.API.Request(del)
		return
	}

	// Execute the action
	del := tgbotapi.NewDeleteMessage(pc.chatID, pc.messageID)
	b.API.Request(del)
	b.executePrefixCommand(pc.chatID, pc.callerID, pc.action, pc.args, pc.targetID)
}

// requestPrefixConfirmation requests confirmation for a prefix command
func (b *Bot) requestPrefixConfirmation(msg *tgbotapi.Message, action, args, targetID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	callerID := telegramSenderID(msg)
	msgID, err := b.sendConfirmation(msg.Chat.ID, callerID, action, args, targetID)
	if err != nil {
		b.Logger.Error("failed to send confirmation", zap.Error(err))
		cancel()
		return
	}

	pc := &pendingConfirmation{
		action:     action,
		args:       args,
		callerID:   callerID,
		targetID:   targetID,
		chatID:     msg.Chat.ID,
		messageID:  msgID,
		timestamp:  time.Now(),
		cancelFunc: cancel,
	}
	b.confirmations.add(msgID, pc)

	// Start timeout goroutine
	go func() {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			// Timeout - clean up
			b.confirmations.remove(msgID)
			del := tgbotapi.NewDeleteMessage(pc.chatID, msgID)
			b.API.Request(del)
		}
	}()
}

// executePrefixCommand executes a prefix command after confirmation
// args contains the full argument string (reason, or duration+reason for tban/mute)
// targetID is the resolved target user ID
func (b *Bot) executePrefixCommand(chatID int64, callerID, action, args, targetID string) {
	banner := b.newBanner()
	parts := strings.Fields(args)

	switch action {
	case "ban":
		resp, _, err := b.Moderation.Ban(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("ban failed", zap.Error(err))
			return
		}
		b.API.Send(tgbotapi.NewMessage(chatID, resp))

	case "dban":
		resp, _, err := b.Moderation.DBan(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("dban failed", zap.Error(err))
			return
		}
		b.API.Send(tgbotapi.NewMessage(chatID, resp))

	case "tban":
		if len(parts) < 2 {
			return
		}
		dur, err := util.ParseDuration(parts[0])
		if err != nil {
			b.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid duration: %s", err)))
			return
		}
		var reason string
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
		resp, _, err := b.Moderation.TBan(banner, callerID, targetID, dur, reason, b.Config)
		if err != nil {
			b.Logger.Error("tban failed", zap.Error(err))
			return
		}
		b.API.Send(tgbotapi.NewMessage(chatID, resp))

	case "sban":
		resp, _, err := b.Moderation.SBan(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("sban failed", zap.Error(err))
			return
		}
		b.API.Send(tgbotapi.NewMessage(chatID, resp))

	case "mute":
		if len(parts) < 2 {
			return
		}
		dur, err := util.ParseDuration(parts[0])
		if err != nil {
			b.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid duration: %s", err)))
			return
		}
		var reason string
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
		resp, _, err := b.Moderation.Mute(banner, callerID, targetID, dur, reason, b.Config)
		if err != nil {
			b.Logger.Error("mute failed", zap.Error(err))
			return
		}
		b.API.Send(tgbotapi.NewMessage(chatID, resp))

	case "warn":
		resp, extras, _, err := b.Warn.Warn(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("warn failed", zap.Error(err))
			return
		}
		b.API.Send(tgbotapi.NewMessage(chatID, resp))
		for _, extra := range extras {
			msg := tgbotapi.NewMessage(chatID, extra)
			msg.DisableWebPagePreview = true
			b.API.Send(msg)
		}
	}
}
