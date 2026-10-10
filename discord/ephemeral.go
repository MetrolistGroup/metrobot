package discord

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

var discordURLPattern = regexp.MustCompile(`https?://[^\s>]+`)

func respondEphemeral(s *bot.Client, i discord.Interaction, content string) {
	s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Content:         suppressDiscordEmbeds(content),
			Flags:           discord.MessageFlagEphemeral | discord.MessageFlagSuppressEmbeds,
			AllowedMentions: &discord.AllowedMentions{Parse: []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}},
		},
	})
}

func respondEphemeralAllowEmbeds(s *bot.Client, i discord.Interaction, content string) {
	s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Content:         content,
			Flags:           discord.MessageFlagEphemeral,
			AllowedMentions: &discord.AllowedMentions{Parse: []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}},
		},
	})
}

func respondPublic(s *bot.Client, i discord.Interaction, content string) {
	s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Content:         suppressDiscordEmbeds(content),
			Flags:           discord.MessageFlagSuppressEmbeds,
			AllowedMentions: &discord.AllowedMentions{},
		},
	})
}

func noPingMessage(content string) *discord.MessageCreate {
	return &discord.MessageCreate{
		Content:         content,
		AllowedMentions: &discord.AllowedMentions{},
	}
}

func respondPublicAllowEmbeds(s *bot.Client, i discord.Interaction, content string) {
	s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Content:         content,
			AllowedMentions: &discord.AllowedMentions{Parse: []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}},
		},
	})
}

func deferResponse(s *bot.Client, i discord.Interaction, ephemeral bool) error {
	flags := discord.MessageFlagSuppressEmbeds
	if ephemeral {
		flags |= discord.MessageFlagEphemeral
	}

	return s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeDeferredCreateMessage,
		Data: discord.MessageCreate{
			Flags: flags,
		},
	})
}

func editDeferredResponse(s *bot.Client, i discord.Interaction, content string) error {
	sanitized := suppressDiscordEmbeds(content)
	_, err := s.Rest.UpdateInteractionResponse(i.ApplicationID(), i.Token(), discord.MessageUpdate{Content: &sanitized, AllowedMentions: &discord.AllowedMentions{Parse: []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}}})
	return err
}

func respondPublicAutoDelete(s *bot.Client, i discord.Interaction, content string, logger *zap.Logger) {
	err := s.Rest.CreateInteractionResponse(i.ID(), i.Token(), discord.InteractionResponse{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: discord.MessageCreate{
			Content:         suppressDiscordEmbeds(content),
			Flags:           discord.MessageFlagSuppressEmbeds,
			AllowedMentions: &discord.AllowedMentions{Parse: []discord.AllowedMentionType{discord.AllowedMentionTypeUsers}},
		},
	})
	if err != nil {
		logger.Error("failed to respond to interaction", zap.Error(err))
		return
	}

	time.AfterFunc(15*time.Minute, func() {
		resp, err := s.Rest.GetInteractionResponse(i.ApplicationID(), i.Token())
		if err != nil {
			return
		}
		s.Rest.DeleteMessage(i.Channel().ID(), resp.ID)
	})
}

func sendReply(s *bot.Client, channelID, messageID, content string, autoDelete bool, logger *zap.Logger) {
	msg, err := s.Rest.CreateMessage(snowflakeID(channelID), discord.MessageCreate{
		Content: suppressDiscordEmbeds(content),
		Flags:   discord.MessageFlagSuppressEmbeds,
		MessageReference: &discord.MessageReference{
			MessageID: snowflakePtr(messageID),
		},
	})
	if err != nil {
		logger.Error("failed to send reply", zap.Error(err))
		return
	}

	if autoDelete {
		time.AfterFunc(15*time.Minute, func() {
			s.Rest.DeleteMessage(snowflakeID(channelID), msg.ID)
		})
	}
}

func sendReplyAllowEmbeds(s *bot.Client, channelID, messageID, content string, autoDelete bool, logger *zap.Logger) {
	msg, err := s.Rest.CreateMessage(snowflakeID(channelID), discord.MessageCreate{
		Content: content,
		MessageReference: &discord.MessageReference{
			MessageID: snowflakePtr(messageID),
		},
	})
	if err != nil {
		logger.Error("failed to send reply", zap.Error(err))
		return
	}

	if autoDelete {
		time.AfterFunc(15*time.Minute, func() {
			s.Rest.DeleteMessage(snowflakeID(channelID), msg.ID)
		})
	}
}

// sendReplyWithDelete sends a reply and deletes it after the specified duration
func sendReplyWithDelete(s *bot.Client, channelID, messageID, content string, duration time.Duration, logger *zap.Logger) {
	msg, err := s.Rest.CreateMessage(snowflakeID(channelID), discord.MessageCreate{
		Content: suppressDiscordEmbeds(content),
		Flags:   discord.MessageFlagSuppressEmbeds,
		MessageReference: &discord.MessageReference{
			MessageID: snowflakePtr(messageID),
		},
	})
	if err != nil {
		logger.Error("failed to send reply", zap.Error(err))
		return
	}

	time.AfterFunc(duration, func() {
		s.Rest.DeleteMessage(snowflakeID(channelID), msg.ID)
	})
}

// sendPermissionError sends an error message about missing permissions and deletes both messages after 5 seconds
func sendPermissionError(s *bot.Client, channelID, originalMsgID string, permission string, logger *zap.Logger) {
	content := fmt.Sprintf("❌ I don't have the required permission: **%s**", permission)
	msg, err := s.Rest.CreateMessage(snowflakeID(channelID), discord.MessageCreate{Content: suppressDiscordEmbeds(content)})
	if err != nil {
		logger.Error("failed to send permission error", zap.Error(err))
		return
	}

	// Delete both messages after 5 seconds
	time.AfterFunc(5*time.Second, func() {
		s.Rest.DeleteMessage(snowflakeID(channelID), msg.ID)
		if originalMsgID != "" {
			s.Rest.DeleteMessage(snowflakeID(channelID), snowflakeID(originalMsgID))
		}
	})
}

func dmUser(s *bot.Client, userID, content string) error {
	ch, err := s.Rest.CreateDMChannel(snowflakeID(userID))
	if err != nil {
		return err
	}
	_, err = s.Rest.CreateMessage(ch.ID(), discord.MessageCreate{
		Content: suppressDiscordEmbeds(content),
		Flags:   discord.MessageFlagSuppressEmbeds,
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
