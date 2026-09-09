package discord

import (
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const kmpNoteSelectCustomID = "note:kmp:question"

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

func kmpNoteComponents(content string, selected int) ([]discordgo.MessageComponent, bool) {
	items := noteFAQItems(content)
	if len(items) == 0 || len(items) > 25 || selected < -1 || selected >= len(items) {
		return nil, false
	}
	options := make([]discordgo.SelectMenuOption, len(items))
	for index, item := range items {
		description := strings.NewReplacer("*", "", "`", "").Replace(strings.Join(strings.Fields(item.answer), " "))
		options[index] = discordgo.SelectMenuOption{
			Label:       truncateRunes(item.question, 100),
			Value:       strconv.Itoa(index),
			Description: truncateRunes(description, 100),
			Default:     index == selected,
		}
	}
	body := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: "# Metrolist-KMP FAQ\nChoose a question below."},
	}
	if selected >= 0 && selected < len(items) {
		body = append(body,
			discordgo.Separator{},
			discordgo.TextDisplay{Content: "## " + items[selected].question + "\n" + items[selected].answer},
		)
	}
	body = append(body, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		CustomID:    kmpNoteSelectCustomID,
		Placeholder: "Choose a question",
		MaxValues:   1,
		Options:     options,
	}}})
	return []discordgo.MessageComponent{discordgo.Container{Components: body}}, true
}

func (b *Bot) sendKMPNoteReply(s *discordgo.Session, channelID, messageID, content string) *discordgo.Message {
	components, ok := kmpNoteComponents(content, -1)
	if !ok {
		return nil
	}
	message := &discordgo.MessageSend{
		Components:      components,
		Flags:           discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Reference:       &discordgo.MessageReference{MessageID: messageID},
	}
	reply, err := s.ChannelMessageSendComplex(channelID, message)
	if err == nil {
		return reply
	}
	message.Reference = nil
	reply, err = s.ChannelMessageSendComplex(channelID, message)
	if err != nil {
		b.Logger.Error("failed to send KMP note", zap.Error(err))
		return nil
	}
	return reply
}

func (b *Bot) respondKMPNote(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) bool {
	components, ok := kmpNoteComponents(content, -1)
	if !ok {
		return false
	}
	flags := discordgo.MessageFlagsIsComponentsV2
	if ephemeral {
		flags |= discordgo.MessageFlagsEphemeral
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Components:      components,
			Flags:           flags,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		},
	}); err != nil {
		b.Logger.Error("failed to respond with KMP note", zap.Error(err))
	}
	return true
}

func (b *Bot) handleKMPNoteComponent(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	data := i.MessageComponentData()
	if data.CustomID != kmpNoteSelectCustomID {
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
	components, ok := kmpNoteComponents(content, selected)
	if !ok {
		respondEphemeral(s, i, "That KMP question is unavailable.")
		return true
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Components:      components,
			Flags:           discordgo.MessageFlagsIsComponentsV2,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		},
	}); err != nil {
		b.Logger.Error("failed to update KMP note", zap.Error(err))
	}
	return true
}
