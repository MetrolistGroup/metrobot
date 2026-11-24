package discord

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

const (
	reminderMinDelay   = 10 * time.Second
	reminderMaxDelay   = 30 * 24 * time.Hour
	reminderMaxPending = 20
	reminderMaxChars   = 400
)

// reminderRetryDelays bounds in-process retries after a failed delivery. The
// row stays stored, so a restart tries again.
var reminderRetryDelays = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}

var reminderUnits = map[byte]time.Duration{
	's': time.Second,
	'm': time.Minute,
	'h': time.Hour,
	'd': 24 * time.Hour,
	'w': 7 * 24 * time.Hour,
}

// parseReminderDelay parses shorthand delay specs like "45s", "10m", "1h30m", "2d", "1w".
// Any syntactically valid delay parses, even an out-of-policy one; the caller enforces bounds.
func parseReminderDelay(spec string) (time.Duration, bool) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" || len(spec) > 32 {
		return 0, false
	}
	var total time.Duration
	for spec != "" {
		i := 0
		for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
			i++
		}
		if i == 0 {
			return 0, false
		}
		n, err := strconv.ParseUint(spec[:i], 10, 32)
		if err != nil {
			return 0, false
		}
		spec = spec[i:]
		if spec == "" {
			return 0, false
		}
		unit, ok := reminderUnits[spec[0]]
		if !ok {
			return 0, false
		}
		spec = spec[1:]
		// Both guards reject only values that would overflow int64; range policy is the caller's job.
		if n > uint64(math.MaxInt64/int64(unit)) {
			return 0, false
		}
		add := time.Duration(n) * unit
		if total > time.Duration(math.MaxInt64)-add {
			return 0, false
		}
		total += add
	}
	return total, true
}

// parseReminderArgs splits the delay spec from the reminder text. Consecutive duration
// tokens are joined into one spec so "1h 30m call mom" behaves like "1h30m call mom".
func parseReminderArgs(args string) (spec, text string) {
	spec, text, _ = strings.Cut(strings.TrimSpace(args), " ")
	// Cut reports found=false for the last token, so the loop tests the token itself.
	for next, rest, _ := strings.Cut(text, " "); isReminderDelayToken(next); next, rest, _ = strings.Cut(text, " ") {
		spec += next
		text = rest
	}
	return strings.TrimSpace(spec), strings.TrimSpace(text)
}

func isReminderDelayToken(token string) bool {
	_, ok := parseReminderDelay(token)
	return ok
}

func (b *Bot) handleRemind(s *bot.Client, m *discord.Message, args string) {
	spec, content := parseReminderArgs(args)
	delay, ok := parseReminderDelay(spec)
	switch {
	case !ok:
		b.kudosText(s, m, "Usage: `>>rmme <when> [text]` (e.g. `>>rmme 45m pizza out`, `>>rmme 1h30m call mom`, `>>rmme 2d`)")
		return
	case delay < reminderMinDelay || delay > reminderMaxDelay:
		b.kudosText(s, m, "Pick a delay between 10 seconds and 30 days.")
		return
	case len([]rune(content)) > reminderMaxChars:
		b.kudosText(s, m, fmt.Sprintf("Keep the reminder under %d characters.", reminderMaxChars))
		return
	}
	userID := m.Author.ID.String()
	count, err := b.DB.CountPendingReminders(userID)
	if err != nil {
		b.Logger.Error("failed to count reminders", zap.Error(err))
		b.kudosText(s, m, "The reminder bank is down, try again in a bit.")
		return
	}
	if count >= reminderMaxPending {
		b.kudosText(s, m, fmt.Sprintf("You already have %d reminders waiting. Let some fire first.", reminderMaxPending))
		return
	}
	due := time.Now().Add(delay)
	channelID := m.ChannelID.String()
	id, err := b.DB.AddReminder(userID, channelID, due.Unix(), content)
	if err != nil {
		b.Logger.Error("failed to save reminder", zap.Error(err))
		b.kudosText(s, m, "The reminder bank is down, try again in a bit.")
		return
	}
	b.scheduleReminder(id, userID, channelID, due, content)
	b.kudosText(s, m, fmt.Sprintf("⏰ Got it, I'll remind you <t:%d:R>.", due.Unix()))
}

// scheduleReminder arms the in-process timer for one stored reminder. Pending rows are
// reloaded from the database on startup, so a stopped timer only delays delivery.
func (b *Bot) scheduleReminder(id int64, userID, channelID string, due time.Time, content string) {
	b.armReminder(id, time.Until(due), func() { b.deliverReminderAttempt(id, userID, channelID, content, 0) })
}

func (b *Bot) armReminder(id int64, delay time.Duration, deliver func()) {
	b.reminderMu.Lock()
	if b.lifecycle != nil && b.lifecycle.Err() != nil {
		b.reminderMu.Unlock()
		return
	}
	if b.reminderTimers == nil {
		b.reminderTimers = make(map[int64]*time.Timer)
	}
	b.reminderTimers[id] = time.AfterFunc(delay, func() {
		b.reminderMu.Lock()
		delete(b.reminderTimers, id)
		b.reminderMu.Unlock()
		if b.lifecycle != nil && b.lifecycle.Err() != nil {
			return
		}
		deliver()
	})
	b.reminderMu.Unlock()
}

// deliverReminder pings the user in the channel where the reminder was set. If that
// channel is gone (or the bot lost access), it falls back to the user's DMs.
// It reports whether Discord confirmed a delivery.
func (b *Bot) deliverReminder(id int64, userID, channelID, content string) bool {
	content = reminderContent(userID, content)
	_, err := b.Client.Rest.CreateMessage(snowflakeID(channelID), discord.MessageCreate{
		Content:         content,
		AllowedMentions: &discord.AllowedMentions{Users: []snowflake.ID{snowflakeID(userID)}},
	})
	if err != nil && reminderSendRejected(err) {
		b.Logger.Info("reminder channel unavailable, falling back to DMs",
			zap.Int64("reminder_id", id), zap.String("channel_id", channelID), zap.Error(err))
		err = dmUser(b.Client, userID, content)
	}
	if err != nil {
		// A lost response may hide a sent message, so an uncertain channel
		// failure is retried later rather than also sent by DM right away.
		b.Logger.Error("failed to deliver reminder",
			zap.Int64("reminder_id", id), zap.String("user_id", userID), zap.Error(err))
		return false
	}
	if err := b.DB.DeleteReminder(id); err != nil {
		b.Logger.Error("failed to delete delivered reminder", zap.Int64("reminder_id", id), zap.Error(err))
	}
	return true
}

func (b *Bot) deliverReminderAttempt(id int64, userID, channelID, content string, attempt int) {
	if b.deliverReminder(id, userID, channelID, content) || attempt >= len(reminderRetryDelays) {
		return
	}
	b.armReminder(id, reminderRetryDelays[attempt], func() {
		b.deliverReminderAttempt(id, userID, channelID, content, attempt+1)
	})
}

// reminderSendRejected reports whether Discord answered with a client error,
// meaning nothing was posted.
func reminderSendRejected(err error) bool {
	var restErr *rest.Error
	return errors.As(err, &restErr) && restErr.Response != nil &&
		restErr.Response.StatusCode >= 400 && restErr.Response.StatusCode < 500
}

func reminderContent(userID, content string) string {
	if strings.TrimSpace(content) == "" {
		return fmt.Sprintf("⏰ <@%s>, this is your reminder.", userID)
	}
	return fmt.Sprintf("⏰ <@%s>, your reminder: %s", userID, content)
}

// RestoreReminders reloads pending reminders after a restart and re-arms their timers.
func (b *Bot) RestoreReminders() error {
	reminders, err := b.DB.GetPendingReminders()
	if err != nil {
		return err
	}
	for _, r := range reminders {
		b.scheduleReminder(r.ID, r.UserID, r.ChannelID, time.Unix(r.DueAt, 0), r.Content)
	}
	b.Logger.Info("reminders restored", zap.Int("count", len(reminders)))
	return nil
}
