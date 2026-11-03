package discord

import (
	"context"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

const (
	garminAICooldown           = 3 * time.Second
	garminAIMaxContent         = 1900
	garminAIContextTTL         = 24 * time.Hour
	garminAIAmbientTTL         = 2 * time.Minute
	garminAIContextMax         = 500
	garminAICompactionMessages = 20
	garminAICompactionTimeout  = 20 * time.Second
	garminAITimeout            = 45 * time.Second
	garminSupportTimeout       = 90 * time.Second
	garminAIMaxImages          = 20
)

func (b *Bot) handleGarminAI(s *bot.Client, m *discord.Message, messages []cmd.GarminAIMessage) {
	b.cancelGarminAIAmbient(m)
	b.handleGarminAIWithMode(s, m, messages, false, 0)
}

func (b *Bot) handleGarminAIWithMode(s *bot.Client, m *discord.Message, messages []cmd.GarminAIMessage, ambient bool, ambientToken uint64) {
	supportCompleted := false
	if !b.garminMessageVisible(m.ChannelID.String(), m.ID.String()) {
		return
	}
	if ambient && !b.garminAIAmbientRequestActive(m, ambientToken) {
		return
	}
	if ambient {
		if _, directed := b.garminAIAmbientContinuation(m, garminUserText(messages)); !directed {
			return
		}
	}
	if len(messages) == 0 || !garminAIMessageHasInput(messages[len(messages)-1]) || b.garminAI == nil {
		return
	}
	if m.ChannelID.String() == garminAppSupportID && b.garminDecisions != nil {
		if !b.beginSupportDecision(m) {
			return
		}
		// The claim outlives request unregistration, so duplicate events cannot replace it.
		defer func() { b.endSupportDecision(m, supportCompleted) }()
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	if !b.registerGarminAIRequest(m, cancelRequest) {
		return
	}
	defer b.unregisterGarminAIRequest(m)
	if !ambient && !b.waitForGarminAICooldown(m.Author.ID.String()) {
		return
	}
	if requestCtx.Err() != nil {
		return
	}
	select {
	case b.garminAISlots <- struct{}{}:
		defer func() { <-b.garminAISlots }()
	default:
		return
	}
	typingDone := make(chan struct{})
	defer close(typingDone)
	b.keepGarminTyping(s, m.ChannelID.String(), typingDone)
	supportMode := m.ChannelID.String() == garminAppSupportID
	messages = b.prepareGarminAIConversation(requestCtx, s, m, messages)
	if requestCtx.Err() != nil {
		return
	}
	if supportMode && !ambient {
		messages = b.rememberGarminAIUnansweredContext(m, messages)
	}
	timeout := garminAITimeout
	if supportMode {
		timeout = garminSupportTimeout
	}
	ctx, cancel := context.WithTimeout(requestCtx, timeout)
	defer cancel()
	result, err := b.runGarminAIWithMode(ctx, s, m, messages, ambient, ambientToken)
	if requestCtx.Err() != nil || !b.garminMessageVisible(m.ChannelID.String(), m.ID.String()) {
		return
	}
	// A failed attempt is handled silently; retain pending questions, never a failure reply.
	supportCompleted = true
	if err != nil {
		b.Logger.Error("Metrobot AI request failed", zap.String("user", m.Author.ID.String()), zap.Error(err))
		return
	}
	if ctx.Err() != nil || result == nil {
		return
	}
	if result.Silent {
		if !result.Unverified && !result.Interacted {
			b.clearGarminAIUnansweredContext(m)
		}
		if result.Interacted {
			if ambient {
				b.rememberGarminAmbientUserContext(m, messages, ambientToken)
			} else {
				b.rememberGarminAIUserContext(m, messages)
			}
		}
		return
	}
	if ambient && !b.garminAIAmbientRequestActive(m, ambientToken) {
		return
	}
	if strings.TrimSpace(result.Answer) == "" {
		return
	}
	if m.ChannelID.String() == garminAppSupportID {
		if !result.VerbatimNote {
			result.Answer = truncateGarminAIResponse(result.Answer)
		}
		conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: result.Answer, Timestamp: time.Now().UTC()})
		supportCompleted = b.sendGarminAppSupportReplyAndRememberIfVisible(s, m, result.Answer, conversation, result.NoteName) != nil
		return
	}
	answer, embed := cmd.ParseGarminReply(result.Answer)
	result.Answer = enforceGarminChannelReply(garminRedirectChannelID(s, m.ChannelID.String()), answer, garminUserText(messages))

	renderedAnswer := renderGarminGuildEmojis(s, snowflakeString(m.GuildID), result.Answer)
	if strings.TrimSpace(renderedAnswer) == "" && strings.TrimSpace(result.Answer) != "" {
		return
	}
	result.Answer = renderedAnswer
	formatted := cmd.FormatGarminReply(formatAndTruncateGarminAIResult(result), embed)
	conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: cmd.GarminReplyPlainText(formatted), Timestamp: time.Now().UTC()})
	if ambient {
		b.sendGarminAmbientReply(s, m, formatted, conversation, ambientToken)
	} else {
		b.sendGarminReplyAndRememberIfVisible(s, m, formatted, conversation)
	}
}

func (b *Bot) keepGarminTyping(s *bot.Client, channelID string, done <-chan struct{}) {
	_ = s.Rest.SendTyping(snowflakeID(channelID))
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = s.Rest.SendTyping(snowflakeID(channelID))
			}
		}
	}()
}

func (b *Bot) claimGarminAICooldown(userID string) bool {
	now := time.Now()
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()

	if lastUsed, ok := b.garminAILastUsed[userID]; ok && now.Sub(lastUsed) < garminAICooldown {
		return false
	}
	b.garminAILastUsed[userID] = now
	return true
}

func (b *Bot) waitForGarminAICooldown(userID string) bool {
	for retry := 0; retry <= 3; retry++ {
		if b.claimGarminAICooldown(userID) {
			return true
		}
		if retry < 3 {
			time.Sleep(time.Second)
		}
	}
	return false
}
