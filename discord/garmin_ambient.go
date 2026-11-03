package discord

import (
	"regexp"
	"strings"
	"time"

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
	if !garminAmbientStop(content) {
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
	if token == 0 || !active || b.garminAIAmbientBusy[key] != token || !time.Now().Before(context.ambientUntil) {
		return false
	}
	return b.garminMessageVisibleLocked(m.ChannelID.String(), m.ID.String()) && b.garminAmbientUninterrupted(m, context.replyID)
}

func (b *Bot) sendGarminAmbientReply(s *bot.Client, m *discord.Message, content string, conversation []cmd.GarminAIMessage, token uint64) *discord.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminAIAmbientRequestActiveLocked(m, token) {
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
			before := strings.TrimSpace(content[:index])
			after := strings.TrimSpace(content[index+len(mention):])
			greeting := strings.ToLower(strings.TrimRight(before, ",!"))
			if before != "" && greeting != "hey" && greeting != "hi" && greeting != "hello" && greeting != "yo" && !strings.HasSuffix(before, ",") {
				continue
			}
			if after != "" && !strings.ContainsRune(",:!?", rune(after[0])) {
				words := strings.Fields(strings.ToLower(after))
				switch words[0] {
				case "is", "was", "has", "had", "does", "seems", "looks", "sucks", "works", "said":
					continue
				}
			}
			prompt := strings.TrimSpace(content[:index] + content[index+len(mention):])
			return strings.TrimSpace(strings.TrimLeft(prompt, ",:;-")), true
		}
	}
	return "", false
}

func garminReplyToBot(s *bot.Client, m *discord.Message) bool {
	return s != nil && s.Caches != nil && s.ID() != 0 && m != nil && m.ReferencedMessage != nil &&
		m.ReferencedMessage.Author.ID == s.ID() && garminAIReferenceInScope(m)
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
	for _, mention := range garminAmbientMentionPattern.FindAllStringSubmatch(m.Content, -1) {
		if mention[1] != botID {
			return true
		}
	}
	return false
}

var garminAmbientMentionPattern = regexp.MustCompile(`<@!?(\d+)>`)

// Only explicit references to the preceding answer qualify without an address.
var garminAmbientFollowupPattern = regexp.MustCompile(`(?i)^(?:what (?:do|did) you mean|(?:can|could) you (?:please )?(?:explain|clarify) (?:that|your (?:answer|reply))|(?:please )?(?:explain|clarify) (?:that|your (?:answer|reply))|tell me more about that|why did you (?:say|suggest|recommend) (?:that|this|it))[.!?]*$`)

func garminAmbientNeedsAnswer(content string) bool {
	content = strings.ToLower(strings.TrimSpace(content))
	if content == "yes" || content == "yes please" || content == "no" {
		return true
	}
	return garminAmbientFollowupPattern.MatchString(content)
}

func garminAmbientStop(content string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(content), ".!?")) {
	case "shut up", "stop replying", "stop talking", "be quiet", "go away", "leave me alone":
		return true
	}
	return false
}

// Require the known bot reply in the cache so gaps and interleaved conversations fail closed.
func (b *Bot) garminAmbientUninterrupted(m *discord.Message, replyID string) bool {
	if m == nil || m.ID == 0 || snowflakeID(replyID) == 0 || m.ID <= snowflakeID(replyID) {
		return false
	}
	b.messageLogMu.Lock()
	defer b.messageLogMu.Unlock()
	found := false
	for _, message := range b.messageLogCache[m.ChannelID.String()] {
		if message.ID == replyID {
			found = message.Bot
		} else if snowflakeID(message.ID) > snowflakeID(replyID) && message.ID != m.ID.String() {
			return false
		}
	}
	return found
}

func (b *Bot) garminAIAmbientContinuation(m *discord.Message, prompt string) ([]cmd.GarminAIMessage, bool) {
	userMessage := b.garminAIUserMessage(m, prompt)
	if b.garminAI == nil || !garminAIMessageHasInput(userMessage) || m == nil || m.MessageReference != nil || m.ReferencedMessage != nil || garminAIAmbientTargetsOtherUser(nil, m) {
		return nil, false
	}
	messages, ok := b.garminAIHistory(m, "", true)
	if !ok || len(messages) == 0 {
		return nil, false
	}
	b.garminAIMu.Lock()
	retained := b.garminAIUserContexts[garminAIUserContextKey(m)]
	b.garminAIMu.Unlock()
	previous := messages[len(messages)-1]
	if previous.Role != "assistant" || previous.Name == garminAIConversationSummaryName || !b.garminAmbientUninterrupted(m, retained.replyID) {
		return nil, false
	}
	if !garminAmbientFollowupPattern.MatchString(strings.TrimSpace(prompt)) && !garminAmbientStop(prompt) && !garminAmbientAnswerToQuestion(previous.Content, prompt) {
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

func garminAmbientAnswerToQuestion(previous, content string) bool {
	if !strings.HasSuffix(strings.TrimSpace(previous), "?") {
		return false
	}
	switch strings.ToLower(strings.Trim(strings.TrimSpace(content), ".!?")) {
	case "yes", "no", "yes please", "no thanks":
		return true
	}
	return false
}
