package discord

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

const youtubeCleanerWebhookName = "Metrobot YouTube Cleaner"

var youtubeURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>()]+`)

func cleanYouTubeSourceIndicators(content string) (string, bool) {
	changed := false
	cleaned := youtubeURLPattern.ReplaceAllStringFunc(content, func(raw string) string {
		urlText := strings.TrimRight(raw, `.,!?;:]}"'`)
		parsed, err := url.Parse(urlText)
		if err != nil || !isYouTubeHost(parsed.Hostname()) {
			return raw
		}

		parts := strings.Split(parsed.RawQuery, "&")
		kept := parts[:0]
		removed := false
		for _, part := range parts {
			key, _, _ := strings.Cut(part, "=")
			decoded, err := url.QueryUnescape(key)
			if err == nil && decoded == "si" {
				removed = true
				continue
			}
			kept = append(kept, part)
		}
		if !removed {
			return raw
		}

		parsed.RawQuery = strings.Join(kept, "&")
		changed = true
		return parsed.String() + raw[len(urlText):]
	})
	return cleaned, changed
}

func isYouTubeHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "youtu.be" || host == "youtube.com" || strings.HasSuffix(host, ".youtube.com")
}

func (b *Bot) repostCleanYouTubeLinks(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	cleaned, changed := cleanYouTubeSourceIndicators(m.Content)
	if !changed || len(m.Attachments) > 0 {
		return false
	}

	webhook, threadID, err := b.youtubeCleanerWebhook(s, m.ChannelID)
	if err != nil {
		b.Logger.Error("failed to get YouTube cleaner webhook", zap.String("channel", m.ChannelID), zap.Error(err))
		return false
	}

	username, avatarURL := m.Author.DisplayName(), m.Author.AvatarURL("")
	if m.Member != nil {
		member := *m.Member
		member.GuildID, member.User = m.GuildID, m.Author
		username, avatarURL = member.DisplayName(), member.AvatarURL("")
	}
	params := &discordgo.WebhookParams{
		Content:         cleaned,
		Username:        username,
		AvatarURL:       avatarURL,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Flags:           m.Flags & discordgo.MessageFlagsSuppressEmbeds,
	}

	var posted *discordgo.Message
	if threadID == "" {
		posted, err = s.WebhookExecute(webhook.ID, webhook.Token, true, params)
	} else {
		posted, err = s.WebhookThreadExecute(webhook.ID, webhook.Token, true, threadID, params)
	}
	if err != nil {
		b.Logger.Error("failed to repost cleaned YouTube link", zap.String("message", m.ID), zap.Error(err))
		return false
	}
	if err := s.ChannelMessageDelete(m.ChannelID, m.ID); err != nil {
		if posted != nil {
			_ = s.WebhookMessageDelete(webhook.ID, webhook.Token, posted.ID)
		}
		b.Logger.Error("failed to delete YouTube source link", zap.String("message", m.ID), zap.Error(err))
		return false
	}
	return true
}

func (b *Bot) youtubeCleanerWebhook(s *discordgo.Session, channelID string) (*discordgo.Webhook, string, error) {
	channel, err := s.State.Channel(channelID)
	if err != nil {
		channel, err = s.Channel(channelID)
	}
	if err != nil {
		return nil, "", err
	}

	threadID := ""
	if channel.IsThread() {
		threadID, channelID = channelID, channel.ParentID
		if channelID == "" {
			return nil, "", fmt.Errorf("thread %s has no parent channel", threadID)
		}
	}

	b.youtubeWebhookMu.Lock()
	defer b.youtubeWebhookMu.Unlock()
	webhooks, err := s.ChannelWebhooks(channelID)
	if err != nil {
		return nil, "", err
	}
	botID := ""
	if s.State.User != nil {
		botID = s.State.User.ID
	}
	for _, webhook := range webhooks {
		if webhook.Name == youtubeCleanerWebhookName && webhook.Type == discordgo.WebhookTypeIncoming && webhook.Token != "" &&
			(botID == "" || webhook.User != nil && webhook.User.ID == botID) {
			return webhook, threadID, nil
		}
	}
	webhook, err := s.WebhookCreate(channelID, youtubeCleanerWebhookName, "")
	return webhook, threadID, err
}
