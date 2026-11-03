package discord

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func enforceGarminChannelReply(channelID, answer, prompt string) string {
	if channelID == garminGeneralID && !garminAIRequestsDetail(prompt) {
		return leadingGarminSentences(answer, 2)
	}
	return answer
}

func garminAIRequestsDetail(prompt string) bool {
	words := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	for _, word := range words {
		switch word {
		case "summarize", "summarise", "summary", "recap", "details", "detailed", "elaborate":
			return true
		}
	}
	return containsAnyGarminPhrase(strings.Join(words, " "), "more detail", "in detail", "break down", "conversation history", "who said what", "what did we talk", "what did i miss", "catch me up")
}

func garminRedirectChannelID(s *bot.Client, channelID string) string {
	if channelID == garminGeneralID {
		return channelID
	}
	if channelID == garminChatThreadID {
		return channelID
	}
	if channel := garminCurrentChannel(s, channelID); channel != nil && snowflakeString(channel.ParentID()) == garminGeneralID {
		return garminGeneralID
	}
	return channelID
}

// leadingGarminSentences keeps #general short while leaving room for a setup and punchline.
func leadingGarminSentences(answer string, limit int) string {
	answer = strings.Join(strings.Fields(strings.TrimSpace(answer)), " ")
	for index, r := range answer {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		next := index + utf8.RuneLen(r)
		if next == len(answer) || (next < len(answer) && answer[next] == ' ') {
			if limit--; limit == 0 {
				answer = answer[:next]
				break
			}
		}
	}
	runes := []rune(answer)
	if len(runes) > 240 {
		answer = strings.TrimSpace(string(runes[:237])) + "..."
	}
	return answer
}

func (b *Bot) sendGarminReply(s *bot.Client, m *discord.Message, content string) *discord.Message {
	content, embed := cmd.ParseGarminReply(content)
	var embeds []discord.Embed
	if embed != nil {
		e := discord.Embed{Title: embed.Title, Description: embed.Description, URL: embed.URL, Color: embed.Color}
		for _, field := range embed.Fields {
			e.Fields = append(e.Fields, discord.EmbedField{Name: field.Name, Value: field.Value, Inline: ptr(field.Inline)})
		}
		if embed.Footer != nil && strings.TrimSpace(embed.Footer.Text) != "" {
			e.Footer = &discord.EmbedFooter{Text: embed.Footer.Text}
		}
		embeds = []discord.Embed{e}
	}
	reply, err := s.Rest.CreateMessage(m.ChannelID, discord.MessageCreate{
		Content: truncateGarminAIResponse(content),
		Embeds:  embeds,
		MessageReference: &discord.MessageReference{
			MessageID: snowflakePtr(m.ID.String()),
		},
		AllowedMentions: &discord.AllowedMentions{},
	})
	if err != nil {
		b.Logger.Error("failed to send Garmin AI reply", zap.Error(err))
		return nil
	}
	return reply
}

func (b *Bot) sendGarminReplyAndRememberIfVisible(s *bot.Client, m *discord.Message, content string, conversation []cmd.GarminAIMessage) *discord.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminMessageVisibleLocked(m.ChannelID.String(), m.ID.String()) {
		return nil
	}
	reply := b.sendGarminReply(s, m, content)
	if reply != nil {
		b.storeGarminAIContextLocked(reply.ID.String(), m, conversation, time.Now())
	}
	return reply
}

func (b *Bot) sendGarminReplyIfVisible(s *bot.Client, m *discord.Message, content string) *discord.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminMessageVisibleLocked(m.ChannelID.String(), m.ID.String()) {
		return nil
	}
	return b.sendGarminReply(s, m, content)
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
	if limit <= 3 {
		return strings.Repeat(".", max(0, limit))
	}

	content = content[:limit-3]
	for !utf8.ValidString(content) {
		content = content[:len(content)-1]
	}
	// Prefer a nearby word boundary without discarding most of a short answer.
	if end := strings.LastIndexFunc(content, unicode.IsSpace); end >= len(content)*3/4 {
		content = content[:end]
	}
	return strings.TrimSpace(content) + "..."
}
func sanitizeGarminInternalToolDisclosure(prompt, answer string) string {
	lowerAnswer := strings.ToLower(answer)
	dnrQuestion := garminDNRPattern.MatchString(prompt)
	if dnrQuestion && strings.Contains(lowerAnswer, "do not respond") {
		return `usually, DNR means "do not resuscitate," a medical instruction. context can change what the acronym means.`
	}
	disclosesTool := garminAIInternalToolNamePattern.MatchString(answer) ||
		(strings.Contains(lowerAnswer, "do not respond") && containsAnyGarminPhrase(lowerAnswer,
			"i use it", "i use that", "used for spam", "spam, bait", "messages that genuinely need no reply", "internal action", "internal tool"))
	if !disclosesTool {
		return answer
	}
	if dnrQuestion {
		return `usually, DNR means "do not resuscitate," a medical instruction. context can change what the acronym means.`
	}
	return "that's not a public command or feature."
}

func garminAISilentAnswer(answer string) bool {
	answer = strings.ToLower(strings.TrimSpace(answer))
	answer = strings.Trim(answer, "`*_.,!\"'(){}[] ")
	answer = strings.NewReplacer("_", " ", "-", " ").Replace(answer)
	return strings.Join(strings.Fields(answer), " ") == "do not respond"
}

func formatGarminAIUsage(result *garminAIResult) string {
	var prefix strings.Builder
	if result.ThinkingDuration > 0 {
		if result.ThinkingDuration < 2*time.Second {
			prefix.WriteString("-# thought briefly\n")
		} else {
			fmt.Fprintf(&prefix, "-# thought for %s\n", result.ThinkingDuration.Round(time.Second))
		}
	}
	if len(result.Skills) > 0 {
		fmt.Fprintf(&prefix, "-# used %d skills\n", len(result.Skills))
	}
	if result.ToolCalls > 0 {
		fmt.Fprintf(&prefix, "-# used %d tools\n", result.ToolCalls)
	}
	if result.MemoryUpdated {
		prefix.WriteString("-# memory updated\n")
	}
	return prefix.String()
}

func normalizeGarminAIAnswer(answer string) string {
	hadInternalMarkup := garminAIControlTokenPattern.MatchString(answer) || garminAITextToolCallPattern.MatchString(answer) || garminAITextActionLinePattern.MatchString(answer)
	answer = garminAIControlTokenPattern.ReplaceAllString(answer, "")
	answer = garminAITextToolCallPattern.ReplaceAllString(answer, "")
	answer = garminAITextActionLinePattern.ReplaceAllString(answer, "")
	if functionStart := strings.Index(strings.ToLower(answer), "<function="); functionStart >= 0 {
		hadInternalMarkup = true
		answer = answer[:functionStart]
	}
	answer = strings.TrimSpace(answer)
	answer = strings.NewReplacer(" — ", ", ", "—", ",", " – ", " - ", "–", "-").Replace(answer)
	lines := strings.Split(answer, "\n")
	for len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "-# ") {
		lines = lines[1:]
	}
	answer = strings.TrimSpace(strings.Join(lines, "\n"))
	lower := strings.ToLower(answer)
	for _, prefix := range []string{"garmin,", "garmin:", "garmin -"} {
		if strings.HasPrefix(lower, prefix) {
			answer = strings.TrimSpace(answer[len(prefix):])
			break
		}
	}
	answerBeforeMemoryFilter := answer
	answer = suppressGarminUserMemoryLanguage(answer)
	if answer == "" && (hadInternalMarkup || answerBeforeMemoryFilter != "") {
		return "got it."
	}
	return answer
}

func suppressGarminUserMemoryLanguage(answer string) string {
	if !garminUserMemorySentence(answer) {
		return answer // Preserve paragraphs and numbered steps when there is nothing to remove.
	}
	var kept []string
	start := 0
	for index, r := range answer {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		next := index + 1
		if next < len(answer) && answer[next] != ' ' && answer[next] != '\n' {
			continue
		}
		sentence := strings.TrimSpace(answer[start:next])
		if sentence != "" && !garminUserMemorySentence(sentence) {
			kept = append(kept, sentence)
		}
		start = next
	}
	if rest := strings.TrimSpace(answer[start:]); rest != "" && !garminUserMemorySentence(rest) {
		kept = append(kept, rest)
	}
	return strings.Join(kept, " ")
}

func garminUserMemorySentence(sentence string) bool {
	lower := strings.ToLower(sentence)
	if garminAIUserMemoryOfferPattern.MatchString(lower) {
		return true
	}
	return containsAnyGarminPhrase(lower,
		"do you want me to save", "would you like me to save", "want me to save", "should i save", "shall i save",
		"do you want me to remember", "would you like me to remember", "want me to remember", "should i remember",
		"i can save that", "i can save this", "i can remember that", "i can remember this", "i can store that", "i can store this",
		"i'll remember", "i will remember", "i've saved", "i have saved", "i saved", "saved that",
		"saved this", "got that saved", "save that for future", "added that to your profile", "add that to your profile", "store that in your profile", "personalization memory",
		"permanent preference", "keep that in mind", "noted for future")
}
