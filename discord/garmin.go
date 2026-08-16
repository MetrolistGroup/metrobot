package discord

import (
	"context"
	"net/url"
	"path/filepath"
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
	garminAIExchanges  = 8
	garminAITimeout    = 45 * time.Second
	garminAIMaxImages  = 4
)

type garminAIContext struct {
	userID       string
	guildID      string
	channelID    string
	messages     []cmd.GarminAIMessage
	expiresAt    time.Time
	ambientUntil time.Time
}

func (b *Bot) handleGarminAI(s *discordgo.Session, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) {
	b.cancelGarminAIAmbient(m)
	b.handleGarminAIWithMode(s, m, messages, false, 0)
}

func (b *Bot) handleGarminAIWithMode(s *discordgo.Session, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage, ambient bool, ambientToken uint64) {
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

	result, err := b.runGarminAIWithMode(ctx, s, m, messages, ambient, ambientToken)
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

	conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: result.Answer})
	var reply *discordgo.Message
	if ambient {
		reply = b.sendGarminAmbientReply(s, m, formatAndTruncateGarminAIResult(result), conversation, ambientToken)
	} else {
		reply = b.sendGarminReply(s, m, formatAndTruncateGarminAIResult(result))
	}
	if reply != nil && !ambient {
		b.rememberGarminAIContext(reply.ID, m, conversation)
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

func copyGarminAIMessages(messages []cmd.GarminAIMessage) []cmd.GarminAIMessage {
	copied := append([]cmd.GarminAIMessage(nil), messages...)
	for index := range copied {
		copied[index].Images = append([]string(nil), copied[index].Images...)
	}
	return copied
}

var garminAIEmojis = map[string]discordgo.Emoji{
	"painfade":             {ID: "1438530502041665727", Name: "painfade", Animated: true, Available: true},
	"nosir":                {ID: "1439242164784595055", Name: "nosir", Available: true},
	"thumbcat":             {ID: "1439308285390880978", Name: "thumbcat", Available: true},
	"hm":                   {ID: "1439319659106013294", Name: "hm", Available: true},
	"thonk":                {ID: "1439346894286360607", Name: "thonk", Available: true},
	"wires":                {ID: "1441063797656911952", Name: "wires", Available: true},
	"waah":                 {ID: "1444970411707203745", Name: "waah", Available: true},
	"monkthonk":            {ID: "1464004867759538429", Name: "monkthonk", Available: true},
	"metrolist":            {ID: "1465017326100545792", Name: "metrolist", Available: true},
	"bwaa":                 {ID: "1468220355947528202", Name: "bwaa", Available: true},
	"skullq":               {ID: "1473960170282549320", Name: "skullq", Available: true},
	"crine":                {ID: "1479034017629339748", Name: "crine", Available: true},
	"brick":                {ID: "1479204945864556594", Name: "brick", Available: true},
	"catstare":             {ID: "1479884829427368150", Name: "catstare", Available: true},
	"speed":                {ID: "1479887846935363644", Name: "speed", Available: true},
	"horror":               {ID: "1479887944230633512", Name: "horror", Available: true},
	"interesting":          {ID: "1479889081017041056", Name: "interesting", Available: true},
	"catfuckyou":           {ID: "1479893113391681687", Name: "catfuckyou", Available: true},
	"catshake":             {ID: "1479893137806721087", Name: "catshake", Available: true},
	"thumb":                {ID: "1481187881946058922", Name: "thumb", Available: true},
	"soggy":                {ID: "1481187936765743134", Name: "soggy", Available: true},
	"trolley":              {ID: "1481188057985187982", Name: "trolley", Available: true},
	"steamhappy":           {ID: "1481188123101626549", Name: "steamhappy", Available: true},
	"colonthree":           {ID: "1481188191104139294", Name: "colonthree", Available: true},
	"trolleyz":             {ID: "1481188261274587217", Name: "trolleyz", Animated: true, Available: true},
	"partygopher":          {ID: "1481188463561674882", Name: "partygopher", Animated: true, Available: true},
	"nyaboom":              {ID: "1481188488107004098", Name: "nyaboom", Available: true},
	"husker":               {ID: "1481188515894267924", Name: "husker", Available: true},
	"husk":                 {ID: "1481188537935331520", Name: "husk", Available: true},
	"hu":                   {ID: "1481188560638836908", Name: "hu", Available: true},
	"blobcatcozy":          {ID: "1481188609251082322", Name: "blobcatcozy", Available: true},
	"blobcatmorningcoffee": {ID: "1481188685377699945", Name: "blobcatmorningcoffee", Available: true},
	"snackstare":           {ID: "1481335353523830794", Name: "snackstare", Available: true},
	"bleh":                 {ID: "1482478193985192059", Name: "bleh", Available: true},
	"wavey":                {ID: "1488926226918670489", Name: "wavey", Animated: true, Available: true},
	"dry":                  {ID: "1489623129503436941", Name: "dry", Available: true},
	"happy":                {ID: "1489623255571501248", Name: "happy", Animated: true, Available: true},
	"cathug":               {ID: "1489623318620274789", Name: "cathug", Available: true},
	"metrolist_tomorrow":   {ID: "1489623377403449354", Name: "metrolist_tomorrow", Available: true},
	"trolleyzoom":          {ID: "1489623472840773753", Name: "trolleyzoom", Animated: true, Available: true},
	"kekw":                 {ID: "1492860470816669697", Name: "kekw", Available: true},
	"folk":                 {ID: "1502640041774678057", Name: "folk", Available: true},
	"emoji_43":             {ID: "1503113745864458341", Name: "emoji_43", Available: true},
	"emoji_44":             {ID: "1505946247075467366", Name: "emoji_44", Available: true},
	"glup":                 {ID: "1526939205476028526", Name: "glup", Available: true},
	"cozystars":            {ID: "1528858301813494001", Name: "cozystars", Available: true},
}

func garminAIEmojiByName(_ *discordgo.Session, _ string, name string) *discordgo.Emoji {
	name = strings.Trim(strings.ToLower(strings.TrimSpace(name)), ":")
	emoji, ok := garminAIEmojis[name]
	if !ok {
		return nil
	}
	return &emoji
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
