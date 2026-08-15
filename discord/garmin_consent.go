package discord

import (
	"strings"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const (
	garminMemoryConsentPrefix = "garmin_memory_consent:"
	garminMemoryDisclosure    = "Before your first Metrobot AI chat, choose Enable personalization or Continue without memory. Both choices let you use Metrobot AI. If enabled, Metrobot may remember stable details you share, such as your preferred name, pronouns, interests, preferences, or community/project role, only to personalize future replies. Metrobot bot admins can review saved profiles to manage this feature. The data is not sold, used for advertising, or used to profit from you. Metrobot will not intentionally save raw chat, transient messages, secrets, health data, or precise locations. You can change this later with `/memory personalization`."
)

func (b *Bot) requireGarminMemoryConsent(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	consent, err := b.DB.GetGarminMemoryConsent("discord", m.Author.ID)
	if err != nil {
		b.Logger.Error("failed to read Garmin memory consent", zap.String("user", m.Author.ID), zap.Error(err))
		b.sendGarminReply(s, m, "I couldn't check your personalization preference right now. Try again in a moment.")
		return true
	}
	if consent.Decided {
		return false
	}

	message := &discordgo.MessageSend{
		Content: garminMemoryDisclosure,
		Reference: &discordgo.MessageReference{
			MessageID: m.ID,
		},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Enable personalization",
					Style:    discordgo.SuccessButton,
					CustomID: garminMemoryConsentCustomID(true, m.Author.ID),
				},
				discordgo.Button{
					Label:    "Continue without memory",
					Style:    discordgo.SecondaryButton,
					CustomID: garminMemoryConsentCustomID(false, m.Author.ID),
				},
			}},
		},
	}
	if _, err := s.ChannelMessageSendComplex(m.ChannelID, message); err != nil {
		message.Reference = nil
		if _, retryErr := s.ChannelMessageSendComplex(m.ChannelID, message); retryErr != nil {
			b.Logger.Error("failed to send Garmin memory consent", zap.String("user", m.Author.ID), zap.Error(retryErr))
		}
	}
	return true
}

func (b *Bot) setGarminMemoryConsent(platform, userID string, enabled bool) error {
	b.garminMemoryMu.Lock()
	defer b.garminMemoryMu.Unlock()
	return b.DB.SetGarminMemoryConsent(platform, userID, enabled)
}

func garminMemoryConsentCustomID(enabled bool, userID string) string {
	action := "disable"
	if enabled {
		action = "enable"
	}
	return garminMemoryConsentPrefix + action + ":" + userID
}

func parseGarminMemoryConsentCustomID(customID string) (enabled bool, userID string, ok bool) {
	if !strings.HasPrefix(customID, garminMemoryConsentPrefix) {
		return false, "", false
	}
	parts := strings.Split(strings.TrimPrefix(customID, garminMemoryConsentPrefix), ":")
	if len(parts) != 2 || normalizeDiscordUserID(parts[1]) == "" {
		return false, "", false
	}
	switch parts[0] {
	case "enable":
		return true, parts[1], true
	case "disable":
		return false, parts[1], true
	default:
		return false, "", false
	}
}

func (b *Bot) handleGarminMemoryConsent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	enabled, targetID, ok := parseGarminMemoryConsentCustomID(i.MessageComponentData().CustomID)
	if !ok {
		return
	}
	if i.Member == nil || i.Member.User == nil || i.Member.User.ID != targetID {
		respondGarminConsentEphemeral(s, i, "Only the user who started this chat can choose its memory setting.")
		return
	}
	if err := b.setGarminMemoryConsent("discord", targetID, enabled); err != nil {
		b.Logger.Error("failed to update Garmin memory consent", zap.String("user", targetID), zap.Error(err))
		respondGarminConsentEphemeral(s, i, "I couldn't save that preference right now. Try again in a moment.")
		return
	}

	content := "Personalization memory is disabled. Metrobot AI still works and will not retain profile details. Send your message again to continue."
	if enabled {
		content = "Personalization memory is enabled for future Metrobot AI chats. Send your message again to continue."
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:         content + " You can change this later with `/memory personalization`.",
			Components:      []discordgo.MessageComponent{},
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		},
	}); err != nil {
		b.Logger.Error("failed to confirm Garmin memory consent", zap.String("user", targetID), zap.Error(err))
	}
}

func respondGarminConsentEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral | discordgo.MessageFlagsSuppressEmbeds,
		},
	})
}
