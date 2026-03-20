package discord

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

var discordURLPattern = regexp.MustCompile(`https?://[^\s>]+`)

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: suppressDiscordEmbeds(content),
			Flags:   discordgo.MessageFlagsEphemeral | discordgo.MessageFlagsSuppressEmbeds,
		},
	})
}

func respondPublic(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: suppressDiscordEmbeds(content),
			Flags:   discordgo.MessageFlagsSuppressEmbeds,
		},
	})
}

func deferResponse(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool) error {
	flags := discordgo.MessageFlagsSuppressEmbeds
	if ephemeral {
		flags |= discordgo.MessageFlagsEphemeral
	}

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: flags,
		},
	})
}

func editDeferredResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	sanitized := suppressDiscordEmbeds(content)
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &sanitized})
	return err
}

func respondPublicAutoDelete(s *discordgo.Session, i *discordgo.InteractionCreate, content string, logger *zap.Logger) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: suppressDiscordEmbeds(content),
			Flags:   discordgo.MessageFlagsSuppressEmbeds,
		},
	})
	if err != nil {
		logger.Error("failed to respond to interaction", zap.Error(err))
		return
	}

	time.AfterFunc(15*time.Minute, func() {
		resp, err := s.InteractionResponse(i.Interaction)
		if err != nil {
			return
		}
		s.ChannelMessageDelete(i.ChannelID, resp.ID)
	})
}

func sendReply(s *discordgo.Session, channelID, messageID, content string, autoDelete bool, logger *zap.Logger) {
	msg, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: suppressDiscordEmbeds(content),
		Flags:   discordgo.MessageFlagsSuppressEmbeds,
		Reference: &discordgo.MessageReference{
			MessageID: messageID,
		},
	})
	if err != nil {
		logger.Error("failed to send reply", zap.Error(err))
		return
	}

	if autoDelete {
		time.AfterFunc(15*time.Minute, func() {
			s.ChannelMessageDelete(channelID, msg.ID)
		})
	}
}

// sendPermissionError sends an error message about missing permissions and deletes both messages after 5 seconds
func sendPermissionError(s *discordgo.Session, channelID, originalMsgID string, permission string, logger *zap.Logger) {
	content := fmt.Sprintf("❌ I don't have the required permission: **%s**", permission)
	msg, err := s.ChannelMessageSend(channelID, suppressDiscordEmbeds(content))
	if err != nil {
		logger.Error("failed to send permission error", zap.Error(err))
		return
	}

	// Delete both messages after 5 seconds
	time.AfterFunc(5*time.Second, func() {
		s.ChannelMessageDelete(channelID, msg.ID)
		if originalMsgID != "" {
			s.ChannelMessageDelete(channelID, originalMsgID)
		}
	})
}

func dmUser(s *discordgo.Session, userID, content string) error {
	ch, err := s.UserChannelCreate(userID)
	if err != nil {
		return err
	}
	_, err = s.ChannelMessageSendComplex(ch.ID, &discordgo.MessageSend{
		Content: suppressDiscordEmbeds(content),
		Flags:   discordgo.MessageFlagsSuppressEmbeds,
	})
	return err
}

func suppressDiscordEmbeds(content string) string {
	var out strings.Builder
	lines := strings.SplitAfter(content, "\n")
	inCodeFence := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeFence = !inCodeFence
			out.WriteString(line)
			continue
		}

		if inCodeFence {
			out.WriteString(line)
			continue
		}

		out.WriteString(suppressDiscordEmbedsLine(line))
	}

	return out.String()
}

func suppressDiscordEmbedsLine(line string) string {
	matches := discordURLPattern.FindAllStringIndex(line, -1)
	if len(matches) == 0 {
		return line
	}

	var out strings.Builder
	last := 0

	for _, match := range matches {
		start, end := match[0], match[1]
		out.WriteString(line[last:start])

		url := line[start:end]
		alreadyWrapped := start > 0 && end < len(line) && line[start-1] == '<' && line[end] == '>'
		if alreadyWrapped {
			out.WriteString(url)
		} else {
			out.WriteString("<")
			out.WriteString(url)
			out.WriteString(">")
		}

		last = end
	}

	out.WriteString(line[last:])
	return out.String()
}
