package discord

import (
	"fmt"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

// DiscordCaseLogger implements the CaseLogger interface for logging cases to Discord
type DiscordCaseLogger struct {
	session   *discordgo.Session
	channelID string
	logger    *zap.Logger
}

// NewDiscordCaseLogger creates a new Discord case logger
func NewDiscordCaseLogger(session *discordgo.Session, channelID string, logger *zap.Logger) *DiscordCaseLogger {
	return &DiscordCaseLogger{
		session:   session,
		channelID: channelID,
		logger:    logger,
	}
}

// LogCaseToDiscord logs a case to the configured Discord channel as an embed
func (d *DiscordCaseLogger) LogCaseToDiscord(c *db.Case, targetName, moderatorName string) error {
	if d.channelID == "" {
		return nil // No log channel configured
	}

	// Build the embed
	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Case #%d", c.CaseNumber),
		Color:       getCaseColor(c.ActionType),
		Timestamp:   time.Unix(c.Timestamp, 0).Format(time.RFC3339),
		Description: fmt.Sprintf("**%s** action performed", capitalize(c.ActionType)),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Platform",
				Value:  capitalize(c.Platform),
				Inline: true,
			},
			{
				Name:   "Target",
				Value:  fmt.Sprintf("%s (%s)", targetName, c.TargetID),
				Inline: true,
			},
			{
				Name:   "Moderator",
				Value:  moderatorName,
				Inline: true,
			},
		},
	}

	// Add reason field if provided
	if c.Reason != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Reason",
			Value: c.Reason,
		})
	}

	// Send the embed
	_, err := d.session.ChannelMessageSendEmbed(d.channelID, embed)
	if err != nil {
		d.logger.Error("failed to log case to Discord", zap.Error(err), zap.Int64("case_number", c.CaseNumber))
		return err
	}

	d.logger.Debug("case logged to Discord", zap.Int64("case_number", c.CaseNumber))
	return nil
}

// getCaseColor returns the appropriate color for an action type
func getCaseColor(actionType string) int {
	switch actionType {
	case "ban":
		return 0xFF0000 // Red
	case "dban":
		return 0xFF4500 // Orange-Red
	case "tban":
		return 0xFFA500 // Orange
	case "sban":
		return 0xFFD700 // Gold
	case "mute":
		return 0x800080 // Purple
	case "warn":
		return 0xFFFF00 // Yellow
	case "unwarn":
		return 0x00FF00 // Green
	default:
		return 0x808080 // Gray
	}
}

// capitalize capitalizes the first letter of a string
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}
