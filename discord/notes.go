package discord

import (
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MetrolistGroup/metrobot/util"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

const kmpNoteSelectCustomID = "note:kmp:question"
const kmpNoteHeader = "# Metrolist-KMP FAQ\nChoose a question below."

func newDiscordNoteMessage(content, prefix string) discord.MessageCreate {
	message := discord.MessageCreate{Content: prefix + content}
	if util.UTF16Len(message.Content) > 2000 {
		message.Content = prefix
		message.Files = []*discord.File{{Name: "note.md", Reader: strings.NewReader(content)}}
	}
	return message
}

func kmpNoteOptionText(text string) string {
	if util.UTF16Len(text) <= 100 {
		return text
	}
	units := 0
	for index, r := range text {
		units += utf16.RuneLen(r)
		if units > 99 {
			return text[:index] + "…"
		}
	}
	return text
}

type noteFAQItem struct {
	question string
	answer   string
}

func noteFAQItems(content string) []noteFAQItem {
	var items []noteFAQItem
	var question string
	var answer []string
	flush := func() {
		if question != "" {
			items = append(items, noteFAQItem{question: question, answer: strings.TrimSpace(strings.Join(answer, "\n"))})
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if heading, ok := strings.CutPrefix(strings.TrimSpace(line), "## "); ok {
			flush()
			question = strings.TrimSpace(heading)
			answer = nil
			continue
		}
		if question != "" {
			answer = append(answer, line)
		}
	}
	flush()
	return items
}

func kmpNoteComponents(content string, selected int) ([]discord.LayoutComponent, bool) {
	items := noteFAQItems(content)
	if len(items) == 0 || len(items) > 25 || selected < -1 || selected >= len(items) {
		return nil, false
	}
	options := make([]discord.StringSelectMenuOption, len(items))
	for index, item := range items {
		// Check every answer before publishing a menu that could later fail on selection.
		if util.UTF16Len(kmpNoteHeader+"## "+item.question+"\n"+item.answer) > 4000 {
			return nil, false
		}
		description := strings.NewReplacer("*", "", "`", "").Replace(strings.Join(strings.Fields(item.answer), " "))
		options[index] = discord.StringSelectMenuOption{
			Label:       kmpNoteOptionText(item.question),
			Value:       strconv.Itoa(index),
			Description: kmpNoteOptionText(description),
			Default:     index == selected,
		}
	}
	body := []discord.ContainerSubComponent{
		discord.TextDisplayComponent{Content: kmpNoteHeader},
	}
	if selected >= 0 && selected < len(items) {
		body = append(body,
			discord.SeparatorComponent{},
			discord.TextDisplayComponent{Content: "## " + items[selected].question + "\n" + items[selected].answer},
		)
	}
	body = append(body, discord.ActionRowComponent{Components: []discord.InteractiveComponent{discord.StringSelectMenuComponent{
		CustomID:    kmpNoteSelectCustomID,
		Placeholder: "Choose a question",
		MaxValues:   1,
		Options:     options,
	}}})
	return []discord.LayoutComponent{discord.ContainerComponent{Components: body}}, true
}

func (b *Bot) sendKMPNoteReply(s *bot.Client, channelID, messageID, content string) *discord.Message {
	components, ok := kmpNoteComponents(content, -1)
	if !ok {
		return nil
	}
	message := &discord.MessageCreate{
		Components:       components,
		Flags:            discord.MessageFlagIsComponentsV2,
		AllowedMentions:  &discord.AllowedMentions{},
		MessageReference: &discord.MessageReference{MessageID: snowflakePtr(messageID)},
	}
	reply, err := s.Rest.CreateMessage(snowflakeID(channelID), *message)
	if err == nil {
		return reply
	}
	message.MessageReference = nil
	reply, err = s.Rest.CreateMessage(snowflakeID(channelID), *message)
	if err != nil {
		b.Logger.Error("failed to send KMP note", zap.Error(err))
		return nil
	}
	return reply
}

func (b *Bot) respondKMPNote(s *bot.Client, i *events.ApplicationCommandInteractionCreate, content string, ephemeral bool) bool {
	components, ok := kmpNoteComponents(content, -1)
	if !ok {
		return false
	}
	flags := discord.MessageFlagIsComponentsV2
	if ephemeral {
		flags |= discord.MessageFlagEphemeral
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Components:      components,
			Flags:           flags,
			AllowedMentions: &discord.AllowedMentions{},
		},
	}); err != nil {
		b.Logger.Error("failed to respond with KMP note", zap.Error(err))
	}
	return true
}

func (b *Bot) handleKMPNoteComponent(s *bot.Client, i *events.ComponentInteractionCreate) bool {
	data := i.StringSelectMenuInteractionData()
	if data.CustomID() != kmpNoteSelectCustomID {
		return false
	}
	if len(data.Values) != 1 {
		respondEphemeral(s, i, "That KMP question is unavailable.")
		return true
	}
	selected, err := strconv.Atoi(data.Values[0])
	if err != nil {
		respondEphemeral(s, i, "That KMP question is unavailable.")
		return true
	}
	content, err := b.Notes.GetNote("kmp")
	if err != nil {
		b.Logger.Error("KMP note error", zap.Error(err))
		respondEphemeral(s, i, "The KMP note is unavailable right now.")
		return true
	}
	if selected < 0 || selected >= len(noteFAQItems(content)) {
		respondEphemeral(s, i, "That KMP question is unavailable.")
		return true
	}
	components, ok := kmpNoteComponents(content, selected)
	if !ok {
		// Send a new response: an existing Components V2 message cannot become plain text.
		message := newDiscordNoteMessage(content, "")
		message.Flags = discord.MessageFlagEphemeral
		message.AllowedMentions = &discord.AllowedMentions{}
		if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
			Type: discord.InteractionResponseTypeCreateMessage,
			Data: message,
		}); err != nil {
			b.Logger.Error("failed to send updated KMP note", zap.Error(err))
		}
		return true
	}
	if err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeUpdateMessage,
		Data: discord.MessageCreate{
			Components:      components,
			Flags:           discord.MessageFlagIsComponentsV2,
			AllowedMentions: &discord.AllowedMentions{},
		},
	}); err != nil {
		b.Logger.Error("failed to update KMP note", zap.Error(err))
	}
	return true
}
