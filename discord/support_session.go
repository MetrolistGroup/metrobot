package discord

import (
	"time"

	"github.com/disgoorg/disgo/discord"
)

// supportSessionTTL keeps a support conversation on the chat model after the asker's latest message.
const supportSessionTTL = 3 * time.Minute

// refreshSupportSession starts or extends the asker's session after the bot replies to them in app-support.
func (b *Bot) refreshSupportSession(m *discord.Message) {
	key := garminAIUserContextKey(m)
	if key == "" || m.ChannelID.String() != garminAppSupportID {
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
