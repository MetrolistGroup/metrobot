package discord

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const (
	garminAICooldown   = 10 * time.Second
	garminAIMaxContent = 1900
	garminAIContextTTL = 30 * time.Minute
	garminAIContextMax = 500
	garminAIExchanges  = 3
)

type garminAIContext struct {
	messages  []cmd.GarminAIMessage
	expiresAt time.Time
}

func (b *Bot) handleGarminAI(s *discordgo.Session, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) {
	if b.garminAI == nil {
		return
	}
	if len(messages) == 0 || strings.TrimSpace(messages[len(messages)-1].Content) == "" {
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

	answer, err := b.garminAI.Ask(ctx, messages)
	if err != nil {
		b.Logger.Error("Garmin AI request failed", zap.String("user", m.Author.ID), zap.Error(err))
		b.sendGarminReply(s, m, "I couldn't answer that right now. Try again in a moment.")
		return
	}

	reply := b.sendGarminReply(s, m, truncateGarminAIResponse(answer))
	if reply != nil {
		conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: answer})
		b.rememberGarminAIContext(reply.ID, conversation)
	}
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

func (b *Bot) sendGarminReply(s *discordgo.Session, m *discordgo.MessageCreate, content string) *discordgo.Message {
	reply, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content: content,
		Reference: &discordgo.MessageReference{
			MessageID: m.ID,
		},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		b.Logger.Error("failed to send Garmin AI reply", zap.Error(err))
		return nil
	}
	return reply
}

func (b *Bot) garminAIContinuation(m *discordgo.MessageCreate, prompt string) ([]cmd.GarminAIMessage, bool) {
	if b.garminAI == nil || strings.TrimSpace(prompt) == "" {
		return nil, false
	}

	referenceID := ""
	if m.MessageReference != nil {
		referenceID = m.MessageReference.MessageID
	} else if m.ReferencedMessage != nil {
		referenceID = m.ReferencedMessage.ID
	}
	if referenceID == "" {
		return nil, false
	}

	b.garminAIMu.Lock()
	context, ok := b.garminAIContexts[referenceID]
	if ok && time.Now().After(context.expiresAt) {
		delete(b.garminAIContexts, referenceID)
		ok = false
	}
	b.garminAIMu.Unlock()
	if !ok {
		return nil, false
	}

	messages := copyGarminAIMessages(context.messages)
	maxHistoryMessages := (garminAIExchanges - 1) * 2
	if len(messages) > maxHistoryMessages {
		messages = messages[len(messages)-maxHistoryMessages:]
	}
	messages = append(messages, cmd.GarminAIMessage{Role: "user", Content: strings.TrimSpace(prompt)})
	return messages, true
}

func (b *Bot) rememberGarminAIContext(messageID string, messages []cmd.GarminAIMessage) {
	if messageID == "" {
		return
	}
	maxMessages := garminAIExchanges * 2
	if len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}

	now := time.Now()
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if b.garminAIContexts == nil {
		b.garminAIContexts = make(map[string]garminAIContext)
	}
	for id, context := range b.garminAIContexts {
		if now.After(context.expiresAt) {
			delete(b.garminAIContexts, id)
		}
	}
	if _, exists := b.garminAIContexts[messageID]; !exists && len(b.garminAIContexts) >= garminAIContextMax {
		oldestID := ""
		var oldestExpiry time.Time
		for id, context := range b.garminAIContexts {
			if oldestID == "" || context.expiresAt.Before(oldestExpiry) {
				oldestID = id
				oldestExpiry = context.expiresAt
			}
		}
		delete(b.garminAIContexts, oldestID)
	}
	b.garminAIContexts[messageID] = garminAIContext{
		messages:  copyGarminAIMessages(messages),
		expiresAt: now.Add(garminAIContextTTL),
	}
}

func copyGarminAIMessages(messages []cmd.GarminAIMessage) []cmd.GarminAIMessage {
	return append([]cmd.GarminAIMessage(nil), messages...)
}

func truncateGarminAIResponse(content string) string {
	content = strings.TrimSpace(content)
	limit := garminAIMaxContent
	if len(content) <= limit {
		return content
	}

	content = content[:limit-3]
	for !utf8.ValidString(content) {
		content = content[:len(content)-1]
	}
	return strings.TrimSpace(content) + "..."
}
