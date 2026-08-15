package discord

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const (
	garminAppSupportOnlyReply = "this channel is only for Metrolist app support."
	garminAppSupportNoNote    = "i don't have that in the saved app-support notes, so i can't answer it here."
)

var garminAppSupportTerms = map[string]struct{}{
	"android": {}, "apk": {}, "app": {}, "backup": {}, "buffer": {},
	"cache": {}, "cast": {}, "crash": {}, "download": {}, "install": {}, "library": {}, "login": {},
	"lyric": {}, "metrolist": {}, "offline": {}, "playback": {}, "playlist": {}, "proxy": {},
	"queue": {}, "restart": {}, "restore": {}, "stream": {}, "sync": {}, "theme": {},
	"update": {}, "version": {}, "vpn": {}, "widget": {}, "youtube": {},
}

var garminAppSupportStopWords = map[string]struct{}{
	"a": {}, "about": {}, "an": {}, "and": {}, "app": {}, "are": {}, "can": {}, "do": {}, "does": {},
	"for": {}, "garmin": {}, "help": {}, "how": {}, "i": {}, "in": {}, "is": {}, "it": {}, "me": {},
	"metrolist": {}, "music": {}, "my": {}, "of": {}, "on": {}, "please": {}, "problem": {}, "support": {},
	"the": {}, "this": {}, "to": {}, "what": {}, "why": {}, "with": {}, "youtube": {},
}

func (b *Bot) runGarminAppSupport(messages []cmd.GarminAIMessage) (*garminAIResult, error) {
	query := garminUserText(messages)
	if !garminAppSupportIntent(query) {
		return &garminAIResult{Answer: garminAppSupportOnlyReply, Skills: map[string]struct{}{}}, nil
	}
	names, err := b.DB.ListNotes()
	if err != nil {
		return nil, fmt.Errorf("listing app support notes: %w", err)
	}
	queryTokens := garminSupportSignificantTokens(query)
	bestScore := 0
	bestContent := ""
	bestTied := false
	for _, name := range names {
		if !garminAppSupportNoteName(name) {
			continue
		}
		content, err := b.Notes.GetNote(name)
		if err != nil {
			return nil, fmt.Errorf("reading app support note %q: %w", name, err)
		}
		score := garminAppSupportNoteScore(query, queryTokens, name, content)
		if score > bestScore {
			bestScore = score
			bestContent = content
			bestTied = false
		} else if score > 0 && score == bestScore {
			bestTied = true
		}
	}
	if bestScore < 4 || bestTied {
		return &garminAIResult{Answer: garminAppSupportNoNote, Skills: map[string]struct{}{}}, nil
	}
	return &garminAIResult{Answer: bestContent, Skills: map[string]struct{}{}}, nil
}

func (b *Bot) handleGarminAppSupport(s *discordgo.Session, m *discordgo.MessageCreate, messages []cmd.GarminAIMessage) {
	result, err := b.runGarminAppSupport(messages)
	if err != nil {
		b.Logger.Error("Metrobot app support request failed", zap.String("user", m.Author.ID), zap.Error(err))
		b.sendGarminReply(s, m, garminAppSupportNoNote)
		return
	}
	reply := b.sendGarminAppSupportReply(s, m, result.Answer)
	if reply != nil {
		conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: result.Answer})
		b.rememberGarminAIContext(reply.ID, m.Author.ID, conversation)
	}
}

func (b *Bot) sendGarminAppSupportReply(s *discordgo.Session, m *discordgo.MessageCreate, content string) *discordgo.Message {
	if len(content) <= garminAIMaxContent {
		return b.sendGarminReply(s, m, content)
	}
	message := &discordgo.MessageSend{
		Reference:       &discordgo.MessageReference{MessageID: m.ID},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Files: []*discordgo.File{{
			Name: "app-support-note.md", ContentType: "text/markdown", Reader: strings.NewReader(content),
		}},
	}
	reply, err := s.ChannelMessageSendComplex(m.ChannelID, message)
	if err == nil {
		return reply
	}
	message.Reference = nil
	message.Files[0].Reader = strings.NewReader(content)
	reply, err = s.ChannelMessageSendComplex(m.ChannelID, message)
	if err != nil {
		b.Logger.Error("failed to send app support note", zap.Error(err))
		return nil
	}
	return reply
}

func garminAppSupportNoteName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "app-support" || strings.HasPrefix(name, "app-support-")
}

func garminAppSupportIntent(content string) bool {
	for _, token := range garminSupportTokens(content) {
		if _, ok := garminAppSupportTerms[token]; ok {
			return true
		}
	}
	return false
}

func garminAppSupportNoteScore(query string, queryTokens map[string]struct{}, name, content string) int {
	normalizedName := strings.Join(garminSupportTokens(name), " ")
	score := 0
	if len(normalizedName) >= 3 && strings.Contains(strings.ToLower(query), normalizedName) {
		score += 10
	}
	nameTokens := garminSupportTokenSet(name)
	contentTokens := garminSupportTokenSet(content)
	for token := range queryTokens {
		if _, ok := nameTokens[token]; ok {
			score += 4
			continue
		}
		if _, ok := contentTokens[token]; ok {
			score++
		}
	}
	return score
}

func garminSupportSignificantTokens(content string) map[string]struct{} {
	tokens := garminSupportTokenSet(content)
	for stopWord := range garminAppSupportStopWords {
		delete(tokens, stopWord)
	}
	return tokens
}

func garminSupportTokenSet(content string) map[string]struct{} {
	tokens := garminSupportTokens(content)
	result := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		result[token] = struct{}{}
	}
	return result
}

func garminSupportTokens(content string) []string {
	fields := strings.FieldsFunc(strings.ToLower(content), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for index, field := range fields {
		fields[index] = garminSupportTokenRoot(field)
	}
	return fields
}

func garminSupportTokenRoot(token string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(token) > len(suffix)+3 && strings.HasSuffix(token, suffix) {
			return strings.TrimSuffix(token, suffix)
		}
	}
	return token
}
