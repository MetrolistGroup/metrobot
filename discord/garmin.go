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
	garminAICooldown   = 3 * time.Second
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
	if !b.waitForGarminAICooldown(m.Author.ID) {
		b.sendGarminReply(s, m, "i'm still rate limited, try again in a sec.")
		return
	}
	select {
	case b.garminAISlots <- struct{}{}:
		defer func() { <-b.garminAISlots }()
	default:
		b.sendGarminReply(s, m, "I'm busy right now. Try again in a moment.")
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
	if emoji := garminAIEmojiOnly(result.Answer); emoji != nil {
		if err := s.MessageReactionAdd(m.ChannelID, m.ID, emoji.APIName()); err != nil {
			b.Logger.Warn("failed to convert emoji-only Garmin response to reaction", zap.Error(err))
		} else {
			return
		}
	}
	result.Answer = enforceGarminChannelReply(m.ChannelID, result.Answer)

	displayResult := *result
	displayResult.Answer = expandGarminAIEmojis(s, m.GuildID, result.Answer)
	reply := b.sendGarminReply(s, m, formatAndTruncateGarminAIResult(&displayResult))
	if reply != nil {
		conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: result.Answer})
		b.rememberGarminAIContext(reply.ID, m.Author.ID, conversation)
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
		"not doing that", "i won't do that", "i wont do that")
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
	if referenceID != "" {
		if context, ok := b.garminAIContexts[referenceID]; ok {
			if now.Before(context.expiresAt) {
				return copyGarminAIMessages(context.messages), true
			}
			delete(b.garminAIContexts, referenceID)
		}
		return nil, false
	}
	if context, ok := b.garminAIUserContexts[userID]; ok {
		if now.Before(context.expiresAt) {
			return copyGarminAIMessages(context.messages), true
		}
		delete(b.garminAIUserContexts, userID)
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

func expandGarminAIEmojis(_ *discordgo.Session, _ string, content string) string {
	if !strings.Contains(content, ":") {
		return content
	}
	replacements := make([]string, 0, len(garminAIEmojis)*2)
	for name, emoji := range garminAIEmojis {
		replacements = append(replacements, ":"+name+":", emoji.MessageFormat())
	}
	if len(replacements) == 0 {
		return content
	}
	return strings.NewReplacer(replacements...).Replace(content)
}

func garminAIEmojiByName(_ *discordgo.Session, _ string, name string) *discordgo.Emoji {
	name = strings.Trim(strings.ToLower(strings.TrimSpace(name)), ":")
	emoji, ok := garminAIEmojis[name]
	if !ok {
		return nil
	}
	return &emoji
}

func garminAIEmojiOnly(content string) *discordgo.Emoji {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, ":") && strings.HasSuffix(content, ":") && strings.Count(content, ":") == 2 {
		return garminAIEmojiByName(nil, "", content)
	}
	if matches := discordgo.EmojiRegex.FindStringSubmatch(content); len(matches) > 0 && matches[0] == content {
		for name, emoji := range garminAIEmojis {
			if emoji.MessageFormat() == content {
				copy := garminAIEmojis[name]
				return &copy
			}
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
