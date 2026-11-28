package discord

import (
	"fmt"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

// handleCookie gives the single mentioned user one cookie; each giver gets one per 24 hours.
func (b *Bot) handleCookie(s *bot.Client, m *discord.Message, giverID string) {
	if len(m.Mentions) != 1 || m.Mentions[0].Bot || m.Mentions[0].ID == m.Author.ID {
		b.kudosText(s, m, "Usage: `>>gc @user`. Give one person a cookie, once a day.")
		return
	}
	receiver := m.Mentions[0].ID.String()
	total, next, ok, err := b.DB.GiveCookie(giverID, receiver, time.Now())
	switch {
	case err != nil:
		b.Logger.Error("failed to give cookie", zap.Error(err))
		b.kudosText(s, m, "The cookie jar is down. Try again in a bit.")
	case !ok:
		b.kudosText(s, m, fmt.Sprintf("You already gave a cookie today. Next one <t:%d:R>.", next.Unix()))
	default:
		b.kudPanel(s, m, "Cookie", fmt.Sprintf("<@%s> gave <@%s> a cookie. They have **%d** cookies now.\n-# One cookie per giver every 24 hours.", giverID, receiver, total))
	}
}
