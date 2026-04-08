package discord

import (
	"fmt"
	"strings"

	"github.com/MetrolistGroup/metrobot/util"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

// executePrefixCommand executes a prefix command
// args contains the full argument string (reason, or duration+reason for tban/mute)
// targetID is the resolved target user ID
func (b *Bot) executePrefixCommand(s *discordgo.Session, channelID, callerID, action, args, targetID string) {
	banner := b.newBanner()
	parts := strings.Fields(args)

	switch action {
	case "ban":
		resp, _, err := b.Moderation.Ban(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("ban failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "dban":
		resp, _, err := b.Moderation.DBan(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("dban failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "tban":
		if len(parts) < 2 {
			return
		}
		dur, err := util.ParseDuration(parts[0])
		if err != nil {
			s.ChannelMessageSend(channelID, fmt.Sprintf("Invalid duration: %s", err))
			return
		}
		var reason string
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
		resp, _, err := b.Moderation.TBan(banner, callerID, targetID, dur, reason, b.Config)
		if err != nil {
			b.Logger.Error("tban failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "sban":
		resp, _, err := b.Moderation.SBan(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("sban failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "mute":
		if len(parts) < 2 {
			return
		}
		dur, err := util.ParseDuration(parts[0])
		if err != nil {
			s.ChannelMessageSend(channelID, fmt.Sprintf("Invalid duration: %s", err))
			return
		}
		var reason string
		if len(parts) > 1 {
			reason = strings.Join(parts[1:], " ")
		}
		resp, _, err := b.Moderation.Mute(banner, callerID, targetID, dur, reason, b.Config)
		if err != nil {
			b.Logger.Error("mute failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)

	case "warn":
		resp, extras, _, err := b.Warn.Warn(banner, callerID, targetID, args, b.Config)
		if err != nil {
			b.Logger.Error("warn failed", zap.Error(err))
			return
		}
		s.ChannelMessageSend(channelID, resp)
		for _, extra := range extras {
			s.ChannelMessageSend(channelID, extra)
		}
	}
}
