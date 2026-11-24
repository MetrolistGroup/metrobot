package discord

import (
	"regexp"
	"time"

	"github.com/disgoorg/disgo/discord"
)

// supportSessionTTL keeps a support conversation on the chat model after the asker's latest message.
const supportSessionTTL = 3 * time.Minute

// refreshSupportSession starts or extends the asker's session after the bot replies to them in app-support.
func (b *Bot) refreshSupportSession(m *discord.Message) {
	key := garminAIUserContextKey(m)
	if key == "" || !b.isSupportChannel(m.ChannelID.String()) {
		return
	}
	now := time.Now()
	b.supportMu.Lock()
	defer b.supportMu.Unlock()
	if b.supportSessions == nil {
		b.supportSessions = make(map[string]time.Time)
	}
	for id, until := range b.supportSessions {
		if !now.Before(until) {
			delete(b.supportSessions, id)
		}
	}
	if len(b.supportSessions) < supportStateMax {
		b.supportSessions[key] = now.Add(supportSessionTTL)
	}
}

// continueSupportSession reports whether m arrives inside the author's active session, and extends it.
func (b *Bot) continueSupportSession(m *discord.Message) bool {
	key := garminAIUserContextKey(m)
	now := time.Now()
	b.supportMu.Lock()
	defer b.supportMu.Unlock()
	until, ok := b.supportSessions[key]
	if !ok || !now.Before(until) {
		delete(b.supportSessions, key)
		return false
	}
	b.supportSessions[key] = now.Add(supportSessionTTL)
	return true
}

func (b *Bot) endSupportSession(m *discord.Message) {
	b.supportMu.Lock()
	defer b.supportMu.Unlock()
	delete(b.supportSessions, garminAIUserContextKey(m))
}

// supportHandoffTTL keeps the bot out of a conversation people are having with each other in app-support.
const supportHandoffTTL = 20 * time.Minute

var supportStopPattern = regexp.MustCompile(`(?i)\b(?:shut (?:up|ur|your)|stop (?:spamming|replying|talking|answering)|go (?:to sleep|away)|be quiet|nobody asked|not you|leave me alone)\b`)

func supportUserKey(m *discord.Message, userID string) string {
	return snowflakeString(m.GuildID) + "\x00" + m.ChannelID.String() + "\x00" + userID
}

// noteSupportHandoff hands m's author, and any human they reply to or mention, over to each other.
// A stop request from the author hands only them off.
func (b *Bot) noteSupportHandoff(m *discord.Message, botID string) {
	if !b.isSupportChannel(m.ChannelID.String()) || m.Author.ID == 0 {
		return
	}
	var users []string
	if m.ReferencedMessage != nil && !m.ReferencedMessage.Author.Bot && m.ReferencedMessage.Author.ID != m.Author.ID && m.ReferencedMessage.Author.ID.String() != botID {
		users = append(users, m.ReferencedMessage.Author.ID.String())
	}
	for _, u := range m.Mentions {
		if !u.Bot && u.ID != m.Author.ID && u.ID.String() != botID {
			users = append(users, u.ID.String())
		}
	}
	if len(users) == 0 && !supportStopPattern.MatchString(m.Content) {
		return
	}
	users = append(users, m.Author.ID.String())
	until := time.Now().Add(supportHandoffTTL)
	b.supportMu.Lock()
	defer b.supportMu.Unlock()
	if b.supportHandoffs == nil {
		b.supportHandoffs = make(map[string]time.Time)
	}
	for id, end := range b.supportHandoffs {
		if !time.Now().Before(end) {
			delete(b.supportHandoffs, id)
		}
	}
	for _, id := range users {
		key := supportUserKey(m, id)
		delete(b.supportSessions, key)
		if len(b.supportHandoffs) < supportStateMax {
			b.supportHandoffs[key] = until
		}
	}
}

// supportHandedOff reports whether m's author is talking with people rather than the bot.
func (b *Bot) supportHandedOff(m *discord.Message) bool {
	b.supportMu.Lock()
	defer b.supportMu.Unlock()
	return time.Now().Before(b.supportHandoffs[garminAIUserContextKey(m)])
}
