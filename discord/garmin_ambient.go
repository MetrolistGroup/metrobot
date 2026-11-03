package discord

import (
	"strings"
	"time"
	"unicode"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
)

func garminAIAmbientEnabled(s *bot.Client, channelID string) bool {
	return garminRedirectChannelID(s, channelID) != garminGeneralID
}

func (b *Bot) tryBeginGarminAIAmbient(m *discord.Message) uint64 {
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
	if _, busy := b.garminAIAmbientBusy[key]; busy || now.Sub(b.garminAILastUsed[m.Author.ID.String()]) < garminAICooldown {
		return 0
	}
	b.garminAIAmbientSeq++
	if b.garminAIAmbientSeq == 0 {
		b.garminAIAmbientSeq++
	}
	token := b.garminAIAmbientSeq
	b.garminAIAmbientBusy[key] = token
	b.garminAILastUsed[m.Author.ID.String()] = now
	return token
}

func (b *Bot) endGarminAIAmbient(m *discord.Message, token uint64) {
	key := garminAIUserContextKey(m)
	b.garminAIMu.Lock()
	if b.garminAIAmbientBusy[key] == token {
		delete(b.garminAIAmbientBusy, key)
	}
	b.garminAIMu.Unlock()
}

func (b *Bot) cancelGarminAIAmbient(m *discord.Message) {
	key := garminAIUserContextKey(m)
	b.garminAIMu.Lock()
	delete(b.garminAIAmbientBusy, key)
	b.garminAIMu.Unlock()
}

func (b *Bot) stopGarminAIAmbient(m *discord.Message, content string) bool {
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

func (b *Bot) garminAIAmbientRequestActive(m *discord.Message, token uint64) bool {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	return b.garminAIAmbientRequestActiveLocked(m, token)
}

func (b *Bot) garminAIAmbientRequestActiveLocked(m *discord.Message, token uint64) bool {
	key := garminAIUserContextKey(m)
	context, active := b.garminAIUserContexts[key]
	return token != 0 && active && b.garminAIAmbientBusy[key] == token && time.Now().Before(context.ambientUntil)
}

func (b *Bot) sendGarminAmbientReply(s *bot.Client, m *discord.Message, content string, conversation []cmd.GarminAIMessage, token uint64) *discord.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminAIAmbientRequestActiveLocked(m, token) || !b.garminMessageVisibleLocked(m.ChannelID.String(), m.ID.String()) {
		return nil
	}
	reply := b.sendGarminReply(s, m, content)
	if reply != nil {
		b.storeGarminAIContextLocked(reply.ID.String(), m, conversation, time.Now())
	}
	return reply
}

func (b *Bot) addGarminAmbientReactions(s *bot.Client, m *discord.Message, reactions []string, token uint64) (bool, error) {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminAIAmbientRequestActiveLocked(m, token) {
		return false, nil
	}
	return addGarminReactions(s, m, reactions)
}

func extractGarminPrompt(s *bot.Client, content string) (string, bool) {
	if prompt, triggered := cmd.ExtractGarminPrompt(content); triggered {
		return prompt, true
	}
	if s == nil || s.Caches == nil || s.ID() == 0 {
		return "", false
	}
	for _, mention := range []string{"<@" + s.ID().String() + ">", "<@!" + s.ID().String() + ">"} {
		if index := strings.Index(content, mention); index >= 0 {
			prompt := strings.TrimSpace(content[:index] + content[index+len(mention):])
			return strings.TrimSpace(strings.TrimLeft(prompt, ",:;-")), true
		}
	}
	return "", false
}

func garminAIAmbientTargetsOtherUser(s *bot.Client, m *discord.Message) bool {
	if m == nil {
		return false
	}
	if m.MentionEveryone || len(m.MentionRoles) > 0 {
		return true
	}
	botID := ""
	if s != nil && s.Caches != nil && s.ID() != 0 {
		botID = s.ID().String()
	}
	for _, mention := range m.Mentions {
		if mention.ID.String() != botID {
			return true
		}
	}
	return false
}

func garminAIAmbientTextReplyEligible(s *bot.Client, m *discord.Message, content string) bool {
	if m == nil {
		return false
	}
	botID := ""
	if s != nil && s.Caches != nil && s.ID() != 0 {
		botID = s.ID().String()
	}
	for _, mention := range m.Mentions {
		if mention.ID.String() == botID {
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

func (b *Bot) garminAIAmbientFallbackReaction(s *bot.Client, m *discord.Message, content string, token uint64) bool {
	content = strings.TrimSpace(content)
	if s == nil || m == nil || content == "" || strings.Contains(content, "?") || strings.HasSuffix(content, "*") || len([]rune(content)) > 100 {
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

func (b *Bot) garminAIAmbientContinuation(m *discord.Message, prompt string) ([]cmd.GarminAIMessage, bool) {
	userMessage := b.garminAIUserMessage(m, prompt)
	if b.garminAI == nil || !garminAIMessageHasInput(userMessage) || m.MessageReference != nil || m.ReferencedMessage != nil {
		return nil, false
	}
	messages, ok := b.garminAIHistory(m, "", true)
	if !ok {
		return nil, false
	}
	return append(messages, userMessage), true
}

func (b *Bot) rememberGarminAmbientUserContext(m *discord.Message, messages []cmd.GarminAIMessage, token uint64) {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminAIAmbientRequestActiveLocked(m, token) {
		return
	}
	b.storeGarminAIContextLocked("", m, messages, time.Now())
}
