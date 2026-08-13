package discord

import (
	"context"
	"net/url"
	"path/filepath"
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
	garminAIContextTTL = 2 * time.Hour
	garminAIContextMax = 500
	garminAIExchanges  = 8
	garminAITimeout    = 45 * time.Second
	garminAIMaxImages  = 4
)

type garminAIContext struct {
	messages  []cmd.GarminAIMessage
	expiresAt time.Time
}

func (b *Bot) handleGarminAI(s *discordgo.Session, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) {
	if b.garminAI == nil {
		b.sendGarminReply(s, m, "Metrobot AI isn't configured right now.")
		return
	}
	if len(messages) == 0 || !garminAIMessageHasInput(messages[len(messages)-1]) {
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
		b.sendGarminReply(s, m, "Give me a few seconds before asking again.")
		return
	}

	typingDone := make(chan struct{})
	defer close(typingDone)
	b.keepGarminTyping(s, m.ChannelID, typingDone)
	ctx, cancel := context.WithTimeout(context.Background(), garminAITimeout)
	defer cancel()

	result, err := b.runGarminAI(ctx, s, m, messages)
	if err != nil {
		b.Logger.Error("Metrobot AI request failed", zap.String("user", m.Author.ID), zap.Error(err))
		b.sendGarminReply(s, m, "I couldn't answer that right now. Try again in a moment.")
		return
	}
	if result.Silent {
		return
	}

	displayResult := *result
	displayResult.Answer = expandGarminAIEmojis(s, m.GuildID, result.Answer)
	reply := b.sendGarminReply(s, m, formatAndTruncateGarminAIResult(&displayResult))
	if reply != nil {
		conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: result.Answer})
		b.rememberGarminAIContext(reply.ID, m.Author.ID, conversation)
	}
}

func (b *Bot) keepGarminTyping(s *discordgo.Session, channelID string, done <-chan struct{}) {
	_ = s.ChannelTyping(channelID)
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = s.ChannelTyping(channelID)
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

func (b *Bot) sendGarminReply(s *discordgo.Session, m *discordgo.MessageCreate, content string) *discordgo.Message {
	reply, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content: content,
		Reference: &discordgo.MessageReference{
			MessageID: m.ID,
		},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		b.Logger.Warn("failed to send Garmin AI reply reference, retrying without it", zap.Error(err))
		reply, err = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Content:         content,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		})
		if err != nil {
			b.Logger.Error("failed to send Garmin AI reply", zap.Error(err))
			return nil
		}
	}
	return reply
}

func (b *Bot) garminAIContinuation(m *discordgo.MessageCreate, prompt string) ([]cmd.GarminAIMessage, bool) {
	userMessage := garminAIUserMessage(m, prompt)
	if b.garminAI == nil || !garminAIMessageHasInput(userMessage) {
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

	messages, ok := b.garminAIHistory(m.Author.ID, referenceID)
	if !ok {
		return nil, false
	}

	maxHistoryMessages := (garminAIExchanges - 1) * 2
	if len(messages) > maxHistoryMessages {
		messages = messages[len(messages)-maxHistoryMessages:]
	}
	messages = append(messages, userMessage)
	return messages, true
}

func (b *Bot) garminAITriggeredConversation(m *discordgo.MessageCreate, prompt string) []cmd.GarminAIMessage {
	userMessage := garminAIUserMessage(m, prompt)
	messages, _ := b.garminAIHistory(m.Author.ID, "")
	maxHistoryMessages := (garminAIExchanges - 1) * 2
	if len(messages) > maxHistoryMessages {
		messages = messages[len(messages)-maxHistoryMessages:]
	}
	return append(messages, userMessage)
}

func (b *Bot) garminAIHistory(userID, referenceID string) ([]cmd.GarminAIMessage, bool) {
	now := time.Now()
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if context, ok := b.garminAIUserContexts[userID]; ok {
		if now.Before(context.expiresAt) {
			return copyGarminAIMessages(context.messages), true
		}
		delete(b.garminAIUserContexts, userID)
	}
	if referenceID != "" {
		if context, ok := b.garminAIContexts[referenceID]; ok {
			if now.Before(context.expiresAt) {
				return copyGarminAIMessages(context.messages), true
			}
			delete(b.garminAIContexts, referenceID)
		}
	}
	return nil, false
}

func garminAIUserMessage(m *discordgo.MessageCreate, prompt string) cmd.GarminAIMessage {
	message := cmd.GarminAIMessage{
		Role:    "user",
		Content: strings.TrimSpace(prompt),
		Images:  garminAIImageURLs(m),
	}
	if message.Content == "" && len(message.Images) > 0 {
		message.Content = "what is in this image?"
	}
	return message
}

func garminAIMessageHasInput(message cmd.GarminAIMessage) bool {
	return strings.TrimSpace(message.Content) != "" || len(message.Images) > 0
}

func garminAIImageURLs(m *discordgo.MessageCreate) []string {
	if m == nil || m.Message == nil {
		return nil
	}
	var images []string
	for _, attachment := range m.Attachments {
		if attachment == nil || !garminAIImageAttachment(attachment) {
			continue
		}
		imageURL := strings.TrimSpace(attachment.URL)
		parsed, err := url.Parse(imageURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			continue
		}
		images = append(images, imageURL)
		if len(images) == garminAIMaxImages {
			break
		}
	}
	return images
}

func garminAIImageAttachment(attachment *discordgo.MessageAttachment) bool {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if strings.HasPrefix(contentType, "image/") && contentType != "image/svg+xml" {
		return true
	}
	switch strings.ToLower(filepath.Ext(attachment.Filename)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	default:
		return false
	}
}

func (b *Bot) rememberGarminAIContext(messageID, userID string, messages []cmd.GarminAIMessage) {
	if messageID == "" || userID == "" {
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
	if b.garminAIUserContexts == nil {
		b.garminAIUserContexts = make(map[string]garminAIContext)
	}
	for id, context := range b.garminAIContexts {
		if now.After(context.expiresAt) {
			delete(b.garminAIContexts, id)
		}
	}
	for id, context := range b.garminAIUserContexts {
		if now.After(context.expiresAt) {
			delete(b.garminAIUserContexts, id)
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
	if _, exists := b.garminAIUserContexts[userID]; !exists && len(b.garminAIUserContexts) >= garminAIContextMax {
		oldestID := ""
		var oldestExpiry time.Time
		for id, context := range b.garminAIUserContexts {
			if oldestID == "" || context.expiresAt.Before(oldestExpiry) {
				oldestID = id
				oldestExpiry = context.expiresAt
			}
		}
		delete(b.garminAIUserContexts, oldestID)
	}
	b.garminAIContexts[messageID] = garminAIContext{
		messages:  copyGarminAIMessages(messages),
		expiresAt: now.Add(garminAIContextTTL),
	}
	b.garminAIUserContexts[userID] = garminAIContext{
		messages:  copyGarminAIMessages(messages),
		expiresAt: now.Add(garminAIContextTTL),
	}
}

func copyGarminAIMessages(messages []cmd.GarminAIMessage) []cmd.GarminAIMessage {
	copied := append([]cmd.GarminAIMessage(nil), messages...)
	for index := range copied {
		copied[index].Images = append([]string(nil), copied[index].Images...)
	}
	return copied
}

var garminAIEmojiNames = map[string]struct{}{
	"husk": {}, "husker": {}, "nyaboom": {}, "colonthree": {}, "steamhappy": {}, "trolley": {},
	"soggy": {}, "thumb": {}, "catshake": {}, "catfuckyou": {}, "interesting": {}, "horror": {},
	"speed": {}, "catstare": {}, "brick": {}, "crine": {}, "skullq": {}, "bwaa": {}, "metrolist": {},
	"monkthonk": {}, "waah": {}, "wires": {}, "thonk": {}, "hm": {}, "thumbcat": {}, "nosir": {},
	"cozystars": {}, "glup": {}, "emoji_44": {}, "emoji_43": {}, "folk": {}, "kekw": {},
	"metrolist_tomorrow": {}, "cathug": {}, "dry": {}, "bleh": {}, "snackstare": {},
	"blobcatmorningcoffee": {}, "blobcatcozy": {}, "hu": {}, "trolleyzoom": {}, "happy": {},
	"wavey": {}, "partygopher": {}, "trolleyz": {}, "painfade": {},
}

func expandGarminAIEmojis(s *discordgo.Session, guildID, content string) string {
	if s == nil || s.State == nil || guildID == "" || !strings.Contains(content, ":") {
		return content
	}
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return content
	}
	s.State.RLock()
	defer s.State.RUnlock()
	replacements := make([]string, 0, len(guild.Emojis)*2)
	for _, emoji := range guild.Emojis {
		if emoji == nil {
			continue
		}
		if _, allowed := garminAIEmojiNames[emoji.Name]; !allowed {
			continue
		}
		replacements = append(replacements, ":"+emoji.Name+":", emoji.MessageFormat())
	}
	if len(replacements) == 0 {
		return content
	}
	return strings.NewReplacer(replacements...).Replace(content)
}

func garminAIEmojiByName(s *discordgo.Session, guildID, name string) *discordgo.Emoji {
	if s == nil || s.State == nil || guildID == "" {
		return nil
	}
	name = strings.Trim(strings.ToLower(strings.TrimSpace(name)), ":")
	if _, allowed := garminAIEmojiNames[name]; !allowed {
		return nil
	}
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return nil
	}
	s.State.RLock()
	defer s.State.RUnlock()
	for _, emoji := range guild.Emojis {
		if emoji != nil && emoji.Name == name && emoji.Available {
			return emoji
		}
	}
	return nil
}

func truncateGarminAIResponse(content string) string {
	return truncateGarminAIResponseTo(content, garminAIMaxContent)
}

func formatAndTruncateGarminAIResult(result *garminAIResult) string {
	prefix := formatGarminAIUsage(result)
	available := garminAIMaxContent - len(prefix)
	if available < 4 {
		return truncateGarminAIResponse(prefix)
	}
	return prefix + truncateGarminAIResponseTo(result.Answer, available)
}

func truncateGarminAIResponseTo(content string, limit int) string {
	content = strings.TrimSpace(content)
	if len(content) <= limit {
		return content
	}

	content = content[:limit-3]
	for !utf8.ValidString(content) {
		content = content[:len(content)-1]
	}
	return strings.TrimSpace(content) + "..."
}
