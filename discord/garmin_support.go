package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const (
	garminAppSupportOnlyReply = "this channel is only for Metrolist app support."
	garminAppSupportNoNote    = "i don't have that in the saved notes, so i can't answer it here."

	garminChromeDownloadReply = "try downloading it with Firefox instead; Chrome sometimes fails to hand the APK off to Android's installer."
	garminDonorRoleReply      = "send your donation proof to a staff member who's currently online, preferably Nyx since they're usually around."
	garminCrashContextReply   = "we need context before we can help: say where it crashed and exactly what you did right before it, plus your app and Android versions."
	garminVirusReply          = "the result is a false positive. do you really think Huorong is a good antivirus? have you ever heard of it? the entire app is open-source, and the build process is open and logged. we have nothing to hide in the app."
)

var garminAppSupportTerms = map[string]struct{}{
	"android": {}, "antivirus": {}, "apk": {}, "app": {}, "backup": {}, "browser": {}, "buffer": {},
	"cache": {}, "cast": {}, "chrome": {}, "crash": {}, "donat": {}, "donate": {}, "donation": {}, "donor": {},
	"download": {}, "firefox": {}, "huorong": {}, "install": {}, "kmp": {}, "library": {}, "login": {}, "lyric": {},
	"malware": {}, "metrolist": {}, "offline": {}, "playback": {}, "playlist": {}, "proof": {}, "proxy": {},
	"queue": {}, "restart": {}, "restore": {}, "role": {}, "sponsor": {}, "stream": {}, "sync": {}, "tester": {},
	"theme": {}, "update": {}, "version": {}, "virus": {}, "virustotal": {}, "vpn": {}, "widget": {}, "youtube": {},
}

var garminAppSupportStopWords = map[string]struct{}{
	"a": {}, "about": {}, "after": {}, "an": {}, "and": {}, "app": {}, "are": {}, "before": {}, "broken": {},
	"can": {}, "do": {}, "does": {}, "every": {}, "fail": {}, "fix": {}, "for": {}, "garmin": {}, "get": {},
	"got": {}, "happen": {}, "help": {}, "how": {}, "i": {}, "in": {}, "is": {}, "issue": {}, "it": {},
	"just": {}, "keep": {}, "me": {}, "metrolist": {}, "music": {}, "my": {}, "never": {}, "not": {}, "of": {},
	"on": {}, "only": {}, "please": {}, "problem": {}, "support": {}, "the": {}, "then": {}, "this": {},
	"time": {}, "to": {}, "try": {}, "two": {}, "what": {}, "when": {}, "while": {}, "why": {}, "with": {},
	"work": {}, "youtube": {},
}

func (b *Bot) runGarminAppSupport(messages []cmd.GarminAIMessage) (*garminAIResult, error) {
	query := garminUserText(messages)
	if !garminAppSupportIntent(query) {
		return &garminAIResult{Answer: garminAppSupportOnlyReply, Skills: map[string]struct{}{}}, nil
	}
	if answer := garminAppSupportSkillAnswer(query); answer != "" {
		return &garminAIResult{Answer: answer, Skills: map[string]struct{}{"support": {}}}, nil
	}
	names, err := b.DB.ListNotes()
	if err != nil {
		return nil, fmt.Errorf("listing app support notes: %w", err)
	}
	queryTokens := garminSupportSignificantTokens(query)
	bestScore := 0
	bestContent := ""
	bestTied := false
	for _, note := range names {
		content, err := b.Notes.GetNote(note.Name)
		if err != nil {
			return nil, fmt.Errorf("reading app support note %q: %w", note.Name, err)
		}
		score := garminAppSupportNoteScore(query, queryTokens, note.Name, note.ShortDesc, content)
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
		b.sendGarminReplyIfVisible(s, m, garminAppSupportNoNote)
		return
	}
	conversation := append(copyGarminAIMessages(messages), cmd.GarminAIMessage{Role: "assistant", Content: result.Answer})
	b.sendGarminAppSupportReplyAndRememberIfVisible(s, m, result.Answer, conversation)
}

func (b *Bot) handleGarminAutomaticAppSupport(s *discordgo.Session, m *discordgo.MessageCreate) {
	prompt := strings.TrimSpace(m.Content)
	messages := []cmd.GarminAIMessage{{Role: "user", Content: prompt}}
	result, err := b.runGarminAppSupport(messages)
	if err != nil {
		b.Logger.Error("automatic Metrobot app support failed", zap.String("user", m.Author.ID), zap.Error(err))
		return
	}
	answer := result.Answer
	if answer == garminAppSupportOnlyReply {
		return
	}
	if answer == garminAppSupportNoNote && b.garminGitHub != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		answer, err = b.findFixedGarminAppSupportIssue(ctx, prompt)
		if err != nil {
			b.Logger.Debug("automatic Metrobot app support lookup failed", zap.Error(err))
			return
		}
	}
	if answer == "" || answer == garminAppSupportNoNote {
		return
	}
	conversation := []cmd.GarminAIMessage{{Role: "user", Content: prompt}, {Role: "assistant", Content: answer}}
	b.sendGarminAppSupportReplyAndRememberIfVisible(s, m, answer, conversation)
}

func (b *Bot) findFixedGarminAppSupportIssue(ctx context.Context, prompt string) (string, error) {
	query := garminAppSupportIssueQuery(prompt)
	if query == "" {
		return "", nil
	}
	result, err := b.garminGitHub.SearchIssues(ctx, query+" is:closed")
	if err != nil {
		return "", err
	}
	return garminFixedAppSupportIssue(prompt, result), nil
}

func garminFixedAppSupportIssue(prompt, result string) string {
	var search struct {
		Items []struct {
			Title       string `json:"title"`
			State       string `json:"state"`
			StateReason string `json:"state_reason"`
			HTMLURL     string `json:"html_url"`
			Labels      []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(result), &search) != nil {
		return ""
	}
	promptTokens := garminSupportSignificantTokens(prompt)
	for _, issue := range search.Items {
		if issue.State != "closed" || issue.StateReason != "completed" || issue.HTMLURL == "" {
			continue
		}
		blocked := false
		for _, label := range issue.Labels {
			name := strings.ToLower(label.Name)
			if name == "duplicate" || name == "wontfix" || name == "question" || name == "feature" || strings.HasPrefix(name, "enhancement") {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		matches := 0
		for token := range garminSupportSignificantTokens(issue.Title) {
			if _, relevant := promptTokens[token]; relevant {
				matches++
			}
		}
		if matches >= 2 || (matches == 1 && len(promptTokens) == 1) {
			return "the official issue tracker has a matching report marked completed: " + issue.Title + " " + issue.HTMLURL
		}
	}
	return ""
}

func garminAppSupportIssueQuery(content string) string {
	significant := garminSupportSignificantTokens(content)
	seen := make(map[string]struct{}, len(significant))
	query := make([]string, 0, 4)
	for _, word := range strings.FieldsFunc(strings.ToLower(content), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		token := garminSupportTokenRoot(word)
		if _, ok := significant[token]; !ok {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		query = append(query, word)
		if len(query) == 4 {
			break
		}
	}
	return strings.Join(query, " ")
}

func garminAppSupportSkillAnswer(content string) string {
	lower := strings.ToLower(content)
	tokens := garminSupportTokenSet(content)
	if garminSupportHasAny(tokens, "virus", "virustotal", "antivirus", "huorong", "malware") {
		return garminVirusReply
	}
	if garminSupportHasAny(tokens, "donat", "donate", "donation", "donor", "sponsor") && garminSupportHasAny(tokens, "role", "supporter", "tester") {
		return garminDonorRoleReply
	}
	if _, crash := tokens["crash"]; crash && !garminCrashReportHasContext(lower) {
		return garminCrashContextReply
	}
	if _, chrome := tokens["chrome"]; chrome && strings.Contains(lower, "download") && containsAnyGarminPhrase(lower, "fail", "stuck", "doesn't", "doesnt", "can't", "cant", "won't", "wont", "not install", "never install", "nothing happen") {
		return garminChromeDownloadReply
	}
	return ""
}

func garminCrashReportHasContext(content string) bool {
	for _, detail := range []string{"crash when", "crashed when", "crashes when", "crash while", "crashed while", "when i ", "while i ", "trying to ", "after tapping", "after clicking", "after opening"} {
		if strings.Contains(content, detail) {
			return true
		}
	}
	return false
}

func garminSupportHasAny(tokens map[string]struct{}, values ...string) bool {
	for _, value := range values {
		if _, ok := tokens[value]; ok {
			return true
		}
	}
	return false
}

func (b *Bot) sendGarminAppSupportReplyAndRememberIfVisible(s *discordgo.Session, m *discordgo.MessageCreate, content string, conversation []cmd.GarminAIMessage) *discordgo.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminMessageVisibleLocked(m.ChannelID, m.ID) {
		return nil
	}
	reply := b.sendGarminAppSupportReply(s, m, content)
	if reply != nil {
		b.storeGarminAIContextLocked(reply.ID, m, conversation, time.Now())
	}
	return reply
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

func garminAppSupportIntent(content string) bool {
	for _, token := range garminSupportTokens(content) {
		if _, ok := garminAppSupportTerms[token]; ok {
			return true
		}
	}
	return false
}

func garminAppSupportNoteScore(query string, queryTokens map[string]struct{}, name, shortDesc, content string) int {
	normalizedName := strings.Join(garminSupportTokens(name), " ")
	score := 0
	if len(normalizedName) >= 3 && strings.Contains(strings.ToLower(query), normalizedName) {
		score += 10
	}
	nameTokens := garminSupportTokenSet(name)
	descriptionTokens := garminSupportTokenSet(shortDesc)
	contentTokens := garminSupportTokenSet(content)
	for token := range queryTokens {
		if _, ok := nameTokens[token]; ok {
			score += 4
			continue
		}
		if _, ok := descriptionTokens[token]; ok {
			score += 2
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
	for _, suffix := range []string{"ing", "ed", "es"} {
		if len(token) > len(suffix)+3 && strings.HasSuffix(token, suffix) {
			return strings.TrimSuffix(token, suffix)
		}
	}
	if len(token) > 4 && strings.HasSuffix(token, "s") && !strings.HasSuffix(token, "ss") && !strings.HasSuffix(token, "us") && !strings.HasSuffix(token, "is") {
		return strings.TrimSuffix(token, "s")
	}
	return token
}
