package discord

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const (
	garminAICooldown   = 3 * time.Second
	garminAIMaxContent = 1900
	garminAIContextTTL = 2 * time.Hour
	garminAIAmbientTTL = 2 * time.Minute
	garminAIContextMax = 500
	garminAIExchanges  = 20
	garminAITimeout    = 45 * time.Second
	garminAIMaxImages  = 20
)

type garminAIContext struct {
	userID       string
	guildID      string
	channelID    string
	messages     []cmd.GarminAIMessage
	expiresAt    time.Time
	ambientUntil time.Time
}

var (
	garminAICustomEmojiPattern     = regexp.MustCompile(`<a?:([A-Za-z0-9_~]+):(\d+)>`)
	garminAICustomShortcodePattern = regexp.MustCompile(`:([A-Za-z_~][A-Za-z0-9_~]{1,63}):`)
)

func (b *Bot) handleGarminAI(s *discordgo.Session, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) {
	b.cancelGarminAIAmbient(m)
	b.handleGarminAIWithMode(s, m, messages, false, 0)
}

func (b *Bot) handleGarminAIWithMode(s *discordgo.Session, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage, ambient bool, ambientToken uint64) {
	if !b.garminMessageVisible(m.ChannelID, m.ID) {
		return
	}
	if ambient && !b.garminAIAmbientRequestActive(m, ambientToken) {
		return
	}
	if len(messages) == 0 || !garminAIMessageHasInput(messages[len(messages)-1]) {
		if ambient {
			return
		}
		b.sendGarminReply(s, m, "Ask me something after `garmin,`.")
		return
	}
	if m.ChannelID == garminAppSupportID {
		if ambient && !garminAppSupportIntent(garminUserText(messages)) {
			return
		}
		b.handleGarminAppSupport(s, m, messages)
		return
	}
	if b.garminAI == nil {
		if ambient {
			return
		}
		b.sendGarminReply(s, m, "Metrobot AI isn't configured right now.")
		return
	}
	if !ambient && !b.waitForGarminAICooldown(m.Author.ID) {
		b.sendGarminReply(s, m, "i'm still rate limited, try again in a sec.")
		return
	}
	select {
	case b.garminAISlots <- struct{}{}:
		defer func() { <-b.garminAISlots }()
	default:
		if ambient {
			return
		}
		b.sendGarminReply(s, m, "I'm busy right now. Try again in a moment.")
		return
	}
	typingDone := make(chan struct{})
	defer close(typingDone)
	b.keepGarminTyping(s, m.ChannelID, typingDone)
	ctx, cancel := context.WithTimeout(context.Background(), garminAITimeout)
	defer cancel()
	if !b.registerGarminAIRequest(m, cancel) {
		return
	}
	defer b.unregisterGarminAIRequest(m)

	result, err := b.runGarminAIWithMode(ctx, s, m, messages, ambient, ambientToken)
	if !b.garminMessageVisible(m.ChannelID, m.ID) {
		return
	}
	if err != nil {
		b.Logger.Error("Metrobot AI request failed", zap.String("user", m.Author.ID), zap.Error(err))
		if ambient {
			return
		}
		b.sendGarminReply(s, m, "I couldn't answer that right now. Try again in a moment.")
		return
	}
	if result.Silent {
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
		if ambient {
			return
		}
		b.sendGarminReply(s, m, "I couldn't produce a useful answer for that.")
		return
	}
	result.Answer = enforceGarminChannelReply(garminRedirectChannelID(s, m.ChannelID), result.Answer)

	renderedAnswer := renderGarminGuildEmojis(s, m.GuildID, result.Answer)
	if strings.TrimSpace(renderedAnswer) == "" && strings.TrimSpace(result.Answer) != "" {
		renderedAnswer = "i couldn't find that emoji."
	}
	result.Answer = renderedAnswer
	conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: result.Answer})
	formatted := formatAndTruncateGarminAIResult(result)
	if ambient {
		b.sendGarminAmbientReply(s, m, formatted, conversation, ambientToken)
	} else {
		b.sendGarminReplyAndRememberIfVisible(s, m, formatted, conversation)
	}
}

func enforceGarminChannelReply(channelID, answer string) string {
	if channelID != garminGeneralID {
		return answer
	}
	answer = firstGarminSentence(answer)
	if answer == "" {
		return answer
	}
	lower := strings.ToLower(answer)
	if containsAnyGarminPhrase(lower, "<#"+garminBotsID+">", "#bots") || garminRefusalAnswer(lower) {
		return answer
	}
	return strings.TrimSpace(answer) + " continue in <#" + garminBotsID + "> if you wanna chat more."
}

func garminRedirectChannelID(s *discordgo.Session, channelID string) string {
	if channelID == garminGeneralID {
		return channelID
	}
	if channelID == garminChatThreadID {
		return channelID
	}
	if channel := garminCurrentChannel(s, channelID); channel != nil && channel.ParentID == garminGeneralID {
		return garminGeneralID
	}
	return channelID
}

func firstGarminSentence(answer string) string {
	answer = strings.Join(strings.Fields(strings.TrimSpace(answer)), " ")
	for index, r := range answer {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		next := index + utf8.RuneLen(r)
		if next == len(answer) || (next < len(answer) && answer[next] == ' ') {
			answer = answer[:next]
			break
		}
	}
	runes := []rune(answer)
	if len(runes) > 180 {
		answer = strings.TrimSpace(string(runes[:177])) + "..."
	}
	return answer
}

func garminRefusalAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	return containsAnyGarminPhrase(answer,
		"i can't do that", "i cant do that", "can't help with that", "cant help with that",
		"not doing that", "i won't do that", "i wont do that", "won't help", "wont help", "not helping",
		"not engaging", "keep it civil", "switch to english", "server rules")
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

func (b *Bot) tryBeginGarminAIAmbient(m *discordgo.MessageCreate) uint64 {
	key := garminAIUserContextKey(m)
	if key == "" {
		return 0
	}
	now := time.Now()
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if b.garminAIAmbientBusy == nil {
		b.garminAIAmbientBusy = make(map[string]uint64)
	}
	if b.garminAILastUsed == nil {
		b.garminAILastUsed = make(map[string]time.Time)
	}
	if _, busy := b.garminAIAmbientBusy[key]; busy || now.Sub(b.garminAILastUsed[m.Author.ID]) < garminAICooldown {
		return 0
	}
	b.garminAIAmbientSeq++
	if b.garminAIAmbientSeq == 0 {
		b.garminAIAmbientSeq++
	}
	token := b.garminAIAmbientSeq
	b.garminAIAmbientBusy[key] = token
	b.garminAILastUsed[m.Author.ID] = now
	return token
}

func (b *Bot) endGarminAIAmbient(m *discordgo.MessageCreate, token uint64) {
	key := garminAIUserContextKey(m)
	b.garminAIMu.Lock()
	if b.garminAIAmbientBusy[key] == token {
		delete(b.garminAIAmbientBusy, key)
	}
	b.garminAIMu.Unlock()
}

func (b *Bot) cancelGarminAIAmbient(m *discordgo.MessageCreate) {
	key := garminAIUserContextKey(m)
	b.garminAIMu.Lock()
	delete(b.garminAIAmbientBusy, key)
	b.garminAIMu.Unlock()
}

func (b *Bot) stopGarminAIAmbient(m *discordgo.MessageCreate, content string) bool {
	if !containsAnyGarminPhrase(strings.ToLower(content), "shut up", "stop replying", "stop talking", "be quiet", "go away", "leave me alone") {
		return false
	}
	key := garminAIUserContextKey(m)
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	context, active := b.garminAIUserContexts[key]
	if !active || time.Now().After(context.ambientUntil) {
		return false
	}
	delete(b.garminAIUserContexts, key)
	delete(b.garminAIAmbientBusy, key)
	return true
}

func (b *Bot) garminAIAmbientRequestActive(m *discordgo.MessageCreate, token uint64) bool {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	return b.garminAIAmbientRequestActiveLocked(m, token)
}

func (b *Bot) garminAIAmbientRequestActiveLocked(m *discordgo.MessageCreate, token uint64) bool {
	key := garminAIUserContextKey(m)
	context, active := b.garminAIUserContexts[key]
	return token != 0 && active && b.garminAIAmbientBusy[key] == token && time.Now().Before(context.ambientUntil)
}

func (b *Bot) sendGarminAmbientReply(s *discordgo.Session, m *discordgo.MessageCreate, content string, conversation []cmd.GarminAIMessage, token uint64) *discordgo.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminAIAmbientRequestActiveLocked(m, token) {
		return nil
	}
	reply := b.sendGarminReply(s, m, content)
	if reply != nil {
		b.storeGarminAIContextLocked(reply.ID, m, conversation, time.Now())
	}
	return reply
}

func (b *Bot) addGarminAmbientReactions(s *discordgo.Session, m *discordgo.MessageCreate, reactions []string, token uint64) (bool, error) {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminAIAmbientRequestActiveLocked(m, token) {
		return false, nil
	}
	return addGarminReactions(s, m, reactions)
}

func extractGarminPrompt(s *discordgo.Session, content string) (string, bool) {
	if prompt, triggered := cmd.ExtractGarminPrompt(content); triggered {
		return prompt, true
	}
	if s == nil || s.State == nil || s.State.User == nil {
		return "", false
	}
	for _, mention := range []string{"<@" + s.State.User.ID + ">", "<@!" + s.State.User.ID + ">"} {
		if index := strings.Index(content, mention); index >= 0 {
			prompt := strings.TrimSpace(content[:index] + content[index+len(mention):])
			return strings.TrimSpace(strings.TrimLeft(prompt, ",:;-")), true
		}
	}
	return "", false
}

func garminAIAmbientTargetsOtherUser(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	if m == nil || m.Message == nil {
		return false
	}
	if m.MentionEveryone || len(m.MentionRoles) > 0 {
		return true
	}
	botID := ""
	if s != nil && s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	for _, mention := range m.Mentions {
		if mention != nil && mention.ID != botID {
			return true
		}
	}
	return false
}

func garminAIAmbientTextReplyEligible(s *discordgo.Session, m *discordgo.MessageCreate, content string) bool {
	if m == nil || m.Message == nil {
		return false
	}
	botID := ""
	if s != nil && s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	for _, mention := range m.Mentions {
		if mention != nil && mention.ID == botID {
			return true
		}
	}

	lower := strings.ToLower(strings.TrimSpace(content))
	if containsAnyGarminPhrase(lower,
		"metrobot,", "metrobot:", "hey metrobot", "yo metrobot", "metrobot can", "metrobot could",
		"metrobot would", "metrobot will", "metrobot what", "metrobot why", "metrobot how") {
		return true
	}
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '\''
	})
	hasSecondPerson := false
	for _, word := range words {
		if word == "you" || word == "your" || word == "yours" || word == "you're" || word == "youre" {
			hasSecondPerson = true
			break
		}
	}
	if len(words) > 0 {
		directQuestion := containsAnyGarminPhrase(words[0],
			"what", "why", "how", "when", "where", "which", "who", "can", "could", "would", "should", "is", "are", "do", "does", "did")
		if (strings.HasSuffix(lower, "?") && (directQuestion || hasSecondPerson)) || (hasSecondPerson && directQuestion) {
			return true
		}
	}
	return containsAnyGarminPhrase(lower, "explain ", "tell me ", "show me ", "go on", "continue", "elaborate")
}

func (b *Bot) garminAIAmbientFallbackReaction(s *discordgo.Session, m *discordgo.MessageCreate, content string, token uint64) bool {
	content = strings.TrimSpace(content)
	if s == nil || m == nil || m.Message == nil || content == "" || strings.Contains(content, "?") || strings.HasSuffix(content, "*") || len([]rune(content)) > 100 {
		return false
	}
	words := strings.Fields(content)
	if len(words) > 12 || garminAIAmbientTargetsOtherUser(s, m) {
		return false
	}
	lower := strings.ToLower(content)
	emojiName := ""
	switch {
	case containsAnyGarminPhrase(lower, "thanks", "thank you", "nice", "cool", "great", "good", "lovely", "alive", "rejoice", "yay", "let's go", "lets go"):
		emojiName = "happy"
	case containsAnyGarminPhrase(lower, "wow", "waow", "interesting", "wild", "damn"):
		emojiName = "interesting"
	case containsAnyGarminPhrase(lower, "ok", "okay", "got it", "sure"):
		emojiName = "thumb"
	case containsAnyGarminPhrase(lower, "lol", "lmao", "lmfao", "haha", "hehe"):
		emojiName = "kekw"
	default:
		return false
	}
	interacted, _ := b.addGarminAmbientReactions(s, m, []string{emojiName}, token)
	return interacted
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

	messages, ok := b.garminAIHistory(m, referenceID, false)
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

func (b *Bot) garminAIAmbientContinuation(m *discordgo.MessageCreate, prompt string) ([]cmd.GarminAIMessage, bool) {
	userMessage := garminAIUserMessage(m, prompt)
	if b.garminAI == nil || !garminAIMessageHasInput(userMessage) || m.MessageReference != nil || m.ReferencedMessage != nil {
		return nil, false
	}
	messages, ok := b.garminAIHistory(m, "", true)
	if !ok {
		return nil, false
	}
	maxHistoryMessages := (garminAIExchanges - 1) * 2
	if len(messages) > maxHistoryMessages {
		messages = messages[len(messages)-maxHistoryMessages:]
	}
	return append(messages, userMessage), true
}

func (b *Bot) garminAITriggeredConversation(m *discordgo.MessageCreate, prompt string) []cmd.GarminAIMessage {
	userMessage := garminAIUserMessage(m, prompt)
	messages, _ := b.garminAIHistory(m, "", true)
	maxHistoryMessages := (garminAIExchanges - 1) * 2
	if len(messages) > maxHistoryMessages {
		messages = messages[len(messages)-maxHistoryMessages:]
	}
	return append(messages, userMessage)
}

func (b *Bot) garminAIHistory(m *discordgo.MessageCreate, referenceID string, ambientOnly bool) ([]cmd.GarminAIMessage, bool) {
	if m == nil || m.Message == nil || m.Author == nil {
		return nil, false
	}
	now := time.Now()
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if referenceID != "" {
		if context, ok := b.garminAIContexts[referenceID]; ok {
			if now.After(context.expiresAt) {
				delete(b.garminAIContexts, referenceID)
				return nil, false
			}
			if context.guildID == m.GuildID && context.channelID == m.ChannelID {
				return copyGarminAIMessages(context.messages), true
			}
		}
		return nil, false
	}
	key := garminAIUserContextKey(m)
	if context, ok := b.garminAIUserContexts[key]; ok {
		if now.Before(context.expiresAt) && (!ambientOnly || now.Before(context.ambientUntil)) {
			return copyGarminAIMessages(context.messages), true
		}
		delete(b.garminAIUserContexts, key)
	}
	return nil, false
}

func garminAIUserContextKey(m *discordgo.MessageCreate) string {
	if m == nil || m.Message == nil || m.Author == nil {
		return ""
	}
	return m.GuildID + "\x00" + m.ChannelID + "\x00" + m.Author.ID
}

func (b *Bot) resetGarminContext(channelID, messageID string) error {
	if b.DB == nil {
		return nil
	}
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if err := b.DB.SetGarminContextCutoff(channelID, messageID); err != nil {
		return err
	}
	if b.garminContextCutoffs == nil {
		b.garminContextCutoffs = make(map[string]string)
	}
	b.garminContextCutoffs[channelID] = messageID
	for key, context := range b.garminAIContexts {
		if context.channelID == channelID {
			delete(b.garminAIContexts, key)
		}
	}
	for key, context := range b.garminAIUserContexts {
		if context.channelID == channelID {
			delete(b.garminAIUserContexts, key)
		}
	}
	keyPart := "\x00" + channelID + "\x00"
	for key := range b.garminAIAmbientBusy {
		if strings.Contains(key, keyPart) {
			delete(b.garminAIAmbientBusy, key)
		}
	}
	for _, cancel := range b.garminAIRequests[channelID] {
		cancel()
	}
	delete(b.garminAIRequests, channelID)
	return nil
}

func (b *Bot) garminMessageVisible(channelID, messageID string) bool {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	return b.garminMessageVisibleLocked(channelID, messageID)
}

func (b *Bot) garminMessageVisibleLocked(channelID, messageID string) bool {
	cutoff, loaded := b.garminContextCutoffs[channelID]
	if !loaded {
		if b.DB == nil {
			return true
		}
		var err error
		cutoff, err = b.DB.GetGarminContextCutoff(channelID)
		if err != nil {
			if b.Logger != nil {
				b.Logger.Error("failed to load Garmin context cutoff", zap.String("channel", channelID), zap.Error(err))
			}
			return false
		}
		if b.garminContextCutoffs == nil {
			b.garminContextCutoffs = make(map[string]string)
		}
		b.garminContextCutoffs[channelID] = cutoff
	}
	if cutoff == "" {
		return true
	}
	messageSnowflake, messageErr := strconv.ParseUint(messageID, 10, 64)
	cutoffSnowflake, cutoffErr := strconv.ParseUint(cutoff, 10, 64)
	return messageErr == nil && cutoffErr == nil && messageSnowflake > cutoffSnowflake
}

func (b *Bot) sendGarminReplyAndRememberIfVisible(s *discordgo.Session, m *discordgo.MessageCreate, content string, conversation []cmd.GarminAIMessage) *discordgo.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminMessageVisibleLocked(m.ChannelID, m.ID) {
		return nil
	}
	reply := b.sendGarminReply(s, m, content)
	if reply != nil {
		b.storeGarminAIContextLocked(reply.ID, m, conversation, time.Now())
	}
	return reply
}

func (b *Bot) sendGarminReplyIfVisible(s *discordgo.Session, m *discordgo.MessageCreate, content string) *discordgo.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminMessageVisibleLocked(m.ChannelID, m.ID) {
		return nil
	}
	return b.sendGarminReply(s, m, content)
}

func (b *Bot) registerGarminAIRequest(m *discordgo.MessageCreate, cancel context.CancelFunc) bool {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminMessageVisibleLocked(m.ChannelID, m.ID) {
		return false
	}
	if b.garminAIRequests == nil {
		b.garminAIRequests = make(map[string]map[string]context.CancelFunc)
	}
	if b.garminAIRequests[m.ChannelID] == nil {
		b.garminAIRequests[m.ChannelID] = make(map[string]context.CancelFunc)
	}
	if previous := b.garminAIRequests[m.ChannelID][m.ID]; previous != nil {
		previous()
	}
	b.garminAIRequests[m.ChannelID][m.ID] = cancel
	return true
}

func (b *Bot) unregisterGarminAIRequest(m *discordgo.MessageCreate) {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	requests := b.garminAIRequests[m.ChannelID]
	delete(requests, m.ID)
	if len(requests) == 0 {
		delete(b.garminAIRequests, m.ChannelID)
	}
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

func (b *Bot) rememberGarminAIContext(messageID string, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) {
	b.storeGarminAIContext(messageID, m, messages)
}

func (b *Bot) rememberGarminAIUserContext(m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) {
	b.storeGarminAIContext("", m, messages)
}

func (b *Bot) rememberGarminAmbientUserContext(m *discordgo.MessageCreate, messages []cmd.GarminAIMessage, token uint64) {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminAIAmbientRequestActiveLocked(m, token) {
		return
	}
	b.storeGarminAIContextLocked("", m, messages, time.Now())
}

func (b *Bot) storeGarminAIContext(messageID string, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) {
	userKey := garminAIUserContextKey(m)
	if userKey == "" {
		return
	}
	maxMessages := garminAIExchanges * 2
	if len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}

	now := time.Now()
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	b.storeGarminAIContextLocked(messageID, m, messages, now)
}

func (b *Bot) storeGarminAIContextLocked(messageID string, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage, now time.Time) {
	userKey := garminAIUserContextKey(m)
	if userKey == "" {
		return
	}
	maxMessages := garminAIExchanges * 2
	if len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}
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
	if messageID != "" {
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
	}
	if _, exists := b.garminAIUserContexts[userKey]; !exists && len(b.garminAIUserContexts) >= garminAIContextMax {
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
	context := garminAIContext{
		userID:       m.Author.ID,
		guildID:      m.GuildID,
		channelID:    m.ChannelID,
		messages:     copyGarminAIMessages(messages),
		expiresAt:    now.Add(garminAIContextTTL),
		ambientUntil: now.Add(garminAIAmbientTTL),
	}
	if messageID != "" {
		b.garminAIContexts[messageID] = context
	}
	b.garminAIUserContexts[userKey] = context
}

func addGarminImagesToLatestUser(messages []cmd.GarminAIMessage, imageURLs []string) {
	if len(imageURLs) == 0 {
		return
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			messages[i].Images = uniqueGarminAIImageURLs(append(append([]string(nil), messages[i].Images...), imageURLs...), garminAIMaxImages)
			return
		}
	}
}

func garminAIEmojiByName(s *discordgo.Session, guildID, name string) (*discordgo.Emoji, bool) {
	emojis, err := garminLiveGuildEmojis(s, guildID)
	if err != nil {
		return nil, false
	}
	return garminEmojiByName(emojis, name)
}

func garminLiveGuildEmojis(s *discordgo.Session, guildID string) ([]*discordgo.Emoji, error) {
	if s != nil && guildID != "" && s.Ratelimiter != nil && s.Client != nil {
		if emojis, err := s.GuildEmojis(guildID); err == nil {
			return emojis, nil
		}
	}
	return garminGuildEmojis(s, guildID)
}

func garminGuildEmojis(s *discordgo.Session, guildID string) ([]*discordgo.Emoji, error) {
	if s == nil || guildID == "" {
		return nil, fmt.Errorf("Discord guild emojis are unavailable")
	}
	if s.State != nil {
		if guild, err := s.State.Guild(guildID); err == nil {
			return guild.Emojis, nil
		}
	}
	if s.Ratelimiter == nil || s.Client == nil {
		return nil, fmt.Errorf("Discord guild emojis are unavailable")
	}
	return s.GuildEmojis(guildID)
}

func garminEmojiByName(emojis []*discordgo.Emoji, name string) (*discordgo.Emoji, bool) {
	name = strings.Trim(strings.TrimSpace(name), ":")
	for _, emoji := range emojis {
		if emoji != nil && emoji.ID != "" && emoji.Available && strings.EqualFold(emoji.Name, name) {
			return emoji, true
		}
	}
	return nil, false
}

func garminEmojiImageURL(emoji *discordgo.Emoji) string {
	extension := "png"
	if emoji.Animated {
		extension = "gif"
	}
	return fmt.Sprintf("https://cdn.discordapp.com/emojis/%s.%s?size=128&quality=lossless", emoji.ID, extension)
}

func renderGarminGuildEmojis(s *discordgo.Session, guildID, answer string) string {
	if !garminAICustomEmojiPattern.MatchString(answer) && !garminAICustomShortcodePattern.MatchString(answer) {
		return answer
	}
	emojis, err := garminLiveGuildEmojis(s, guildID)
	if err != nil {
		answer = garminAICustomEmojiPattern.ReplaceAllString(answer, "")
		answer = garminAICustomShortcodePattern.ReplaceAllString(answer, "")
		return strings.TrimSpace(answer)
	}

	placeholders := make([]string, 0)
	replaceEmoji := func(name string) string {
		emoji, ok := garminEmojiByName(emojis, name)
		if !ok {
			return ""
		}
		placeholder := fmt.Sprintf("\x00GARMIN_EMOJI_%d\x00", len(placeholders))
		placeholders = append(placeholders, emoji.MessageFormat())
		return placeholder
	}
	answer = garminAICustomEmojiPattern.ReplaceAllStringFunc(answer, func(markup string) string {
		return replaceEmoji(garminAICustomEmojiPattern.FindStringSubmatch(markup)[1])
	})
	answer = garminAICustomShortcodePattern.ReplaceAllStringFunc(answer, func(shortcode string) string {
		return replaceEmoji(garminAICustomShortcodePattern.FindStringSubmatch(shortcode)[1])
	})
	for i, emoji := range placeholders {
		answer = strings.ReplaceAll(answer, fmt.Sprintf("\x00GARMIN_EMOJI_%d\x00", i), emoji)
	}
	for strings.Contains(answer, "  ") {
		answer = strings.ReplaceAll(answer, "  ", " ")
	}
	return strings.TrimSpace(answer)
}

func copyGarminAIMessages(messages []cmd.GarminAIMessage) []cmd.GarminAIMessage {
	copied := append([]cmd.GarminAIMessage(nil), messages...)
	for index := range copied {
		copied[index].Images = append([]string(nil), copied[index].Images...)
	}
	return copied
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
