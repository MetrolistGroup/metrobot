package discord

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const (
	garminAICooldown   = 10 * time.Second
	garminAIMaxContent = 1900
)

func (b *Bot) handleGarminAI(s *discordgo.Session, m *discordgo.MessageCreate, prompt string) {
	if b.garminAI == nil {
		return
	}
	if prompt == "" {
		b.sendGarminReply(s, m, "Ask me something after `garmin,`.")
		return
	}
	select {
	case b.garminAISlots <- struct{}{}:
		defer func() { <-b.garminAISlots }()
	default:
		b.sendGarminReply(s, m, "I'm busy right now. Try again in a moment.")
		return
	}
	if !b.claimGarminAICooldown(m.Author.ID) {
		return
	}

	_ = s.ChannelTyping(m.ChannelID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answer, err := b.garminAI.Ask(ctx, prompt)
	if err != nil {
		b.Logger.Error("Garmin AI request failed", zap.String("user", m.Author.ID), zap.Error(err))
		b.sendGarminReply(s, m, "I couldn't answer that right now. Try again in a moment.")
		return
	}

	footer := garminAIFooter(b.garminAI.Attribution())
	b.sendGarminReply(s, m, truncateGarminAIResponse(answer, footer)+footer)
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

func (b *Bot) sendGarminReply(s *discordgo.Session, m *discordgo.MessageCreate, content string) {
	_, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content: content,
		Reference: &discordgo.MessageReference{
			MessageID: m.ID,
		},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		b.Logger.Error("failed to send Garmin AI reply", zap.Error(err))
	}
}

func garminAIFooter(attribution string) string {
	return "\n\n-# This response was generated using " + attribution
}

func truncateGarminAIResponse(content, footer string) string {
	content = strings.TrimSpace(content)
	limit := garminAIMaxContent - len(footer)
	if len(content) <= limit {
		return content
	}

	content = content[:limit-3]
	for !utf8.ValidString(content) {
		content = content[:len(content)-1]
	}
	return strings.TrimSpace(content) + "..."
}
