package discord

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

const garminAppSupportTriagePrompt = `You are the silent router for Metrolist's #app-support channel. Decide what should happen to the final current user message by calling exactly one supplied action.

Hand it to the support agent only when the author is directly asking for Metrolist help or information and an automated answer would be useful. Ignore messages addressed to another person, donation-proof or access submissions that require staff action, staff coordination, ordinary conversation, reactions such as "insane", spam, commands, and messages with no clear request. A support-related keyword by itself is never enough. The support agent can inspect saved notes and official issues, but cannot grant roles or tester access. Never write a text response.`

var garminAppSupportTriageTools = []cmd.GarminAITool{
	garminTool("handoff_to_app_support_agent", "Hand the final current message to the Metrolist support agent. Use only for a clear request that the agent can usefully answer.", `{"type":"object","properties":{},"additionalProperties":false}`),
	garminTool("do_not_respond", "Ignore the final current message without replying or reacting.", `{"type":"object","properties":{},"additionalProperties":false}`),
}

var garminAppSupportTerms = map[string]struct{}{
	"android": {}, "antivirus": {}, "apk": {}, "app": {}, "backup": {}, "browser": {}, "buffer": {},
	"cache": {}, "cast": {}, "chrome": {}, "crash": {}, "donat": {}, "donate": {}, "donation": {}, "donor": {},
	"download": {}, "firefox": {}, "huorong": {}, "install": {}, "kmp": {}, "library": {}, "login": {}, "lyric": {},
	"malware": {}, "metrolist": {}, "offline": {}, "playback": {}, "playlist": {}, "proof": {}, "proxy": {},
	"queue": {}, "restart": {}, "restore": {}, "role": {}, "sponsor": {}, "stream": {}, "sync": {}, "tester": {},
	"theme": {}, "update": {}, "version": {}, "virus": {}, "virustotal": {}, "vpn": {}, "widget": {}, "youtube": {},
}

func (b *Bot) handleGarminAutomaticAppSupport(s *bot.Client, m *discord.Message, messages []cmd.GarminAIMessage) {
	if b.garminDecisions != nil {
		b.handleGarminSupportDecision(s, m, messages)
		return
	}
	if b.garminAI == nil {
		return
	}

	handoff, err := func() (bool, error) {
		if b.garminAISlots != nil {
			select {
			case b.garminAISlots <- struct{}{}:
				defer func() { <-b.garminAISlots }()
			default:
				return false, nil
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if !b.registerGarminAIRequest(m, cancel) {
			return false, nil
		}
		defer b.unregisterGarminAIRequest(m)
		return b.runGarminAppSupportTriage(ctx, s, m, messages)
	}()
	if err != nil {
		b.Logger.Debug("automatic Metrobot app-support triage failed", zap.String("user", m.Author.ID.String()), zap.Error(err))
		return
	}
	if handoff && b.garminMessageVisible(m.ChannelID.String(), m.ID.String()) {
		b.handleGarminAI(s, m, messages)
	}
}

func (b *Bot) runGarminAppSupportTriage(ctx context.Context, s *bot.Client, m *discord.Message, messages []cmd.GarminAIMessage) (bool, error) {
	completion, err := b.garminAI.Complete(ctx, cmd.GarminAIRequest{
		SystemPrompt: garminAppSupportTriagePrompt,
		Context:      b.garminDiscordContextForConversation(s, m, messages),
		Messages:     messages,
		Tools:        garminAppSupportTriageTools,
		ToolChoice:   "required",
	})
	if err != nil {
		return false, err
	}
	if completion == nil {
		return false, fmt.Errorf("app-support triage returned no completion")
	}
	for _, call := range completion.Message.ToolCalls {
		if call.Function.Name == "do_not_respond" {
			return false, nil
		}
	}
	for _, call := range completion.Message.ToolCalls {
		if call.Function.Name == "handoff_to_app_support_agent" {
			return true, nil
		}
	}
	return false, nil
}

func (b *Bot) sendGarminAppSupportReplyAndRememberIfVisible(s *bot.Client, m *discord.Message, content string, conversation []cmd.GarminAIMessage, noteName string) *discord.Message {
	b.garminAIMu.Lock()
	defer b.garminAIMu.Unlock()
	if !b.garminMessageVisibleLocked(m.ChannelID.String(), m.ID.String()) {
		return nil
	}
	reply, delivered := b.sendGarminAppSupportReply(s, m, content, noteName)
	if reply != nil {
		conversation = copyGarminAIMessages(conversation)
		if len(conversation) > 0 && conversation[len(conversation)-1].Role == "assistant" {
			conversation[len(conversation)-1].Content = delivered
		}
		b.storeGarminAIContextLocked(reply.ID.String(), m, conversation, time.Now())
	}
	return reply
}

func (b *Bot) sendGarminAppSupportReply(s *bot.Client, m *discord.Message, content, noteName string) (*discord.Message, string) {
	if noteName != "" && b.Notes != nil {
		if note, err := b.Notes.GetNoteDocument(noteName); err == nil {
			rendered := note.Render()
			return b.sendNoteReply(s, m.ChannelID.String(), m.ID.String(), noteName, rendered), rendered.Text
		}
	}
	if len(content) <= garminAIMaxContent {
		return b.sendGarminReply(s, m, content), content
	}
	message := &discord.MessageCreate{
		MessageReference: &discord.MessageReference{MessageID: snowflakePtr(m.ID.String())},
		AllowedMentions:  &discord.AllowedMentions{},
		Files: []*discord.File{{
			Name: "app-support-note.md", Reader: strings.NewReader(content),
		}},
	}
	reply, err := s.Rest.CreateMessage(m.ChannelID, *message)
	if err == nil {
		return reply, content
	}
	if noteRequestRejected(err) {
		message.MessageReference = nil
		message.Files[0].Reader = strings.NewReader(content)
		reply, err = s.Rest.CreateMessage(m.ChannelID, *message)
	}
	if err != nil {
		b.Logger.Error("failed to send app support note", zap.Error(err))
		return nil, ""
	}
	return reply, content
}

func garminAppSupportIntent(content string) bool {
	for _, token := range garminSupportTokens(content) {
		if _, ok := garminAppSupportTerms[token]; ok {
			return true
		}
	}
	return false
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
