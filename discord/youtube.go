package discord

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
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

func (b *Bot) repostCleanYouTubeLinks(s *bot.Client, m *discord.Message) bool {
	cleaned, changed := cleanYouTubeSourceIndicators(m.Content)
	if !changed || len(m.Attachments) > 0 {
		return false
	}

	webhook, threadID, err := b.messageWebhook(s, m.ChannelID.String(), youtubeCleanerWebhookName)
	if err != nil {
		b.Logger.Error("failed to get YouTube cleaner webhook", zap.String("channel", m.ChannelID.String()), zap.Error(err))
		return false
	}

	username, avatarURL := m.Author.EffectiveName(), m.Author.EffectiveAvatarURL()
	if m.Member != nil {
		member := *m.Member
		member.GuildID, member.User = value(m.GuildID), m.Author
		username, avatarURL = member.EffectiveName(), member.EffectiveAvatarURL()
	}
	params := &discord.WebhookMessageCreate{
		Content:         cleaned,
		Username:        username,
		AvatarURL:       avatarURL,
		AllowedMentions: &discord.AllowedMentions{},
		Flags:           m.Flags & discord.MessageFlagSuppressEmbeds,
	}

	posted, err := s.Rest.CreateWebhookMessage(webhook.ID(), webhook.Token, *params, rest.CreateWebhookMessageParams{Wait: true, ThreadID: snowflakeID(threadID)})
	if err != nil {
		b.Logger.Error("failed to repost cleaned YouTube link", zap.String("message", m.ID.String()), zap.Error(err))
		return false
	}
	if err := s.Rest.DeleteMessage(m.ChannelID, m.ID); err != nil {
		if posted != nil {
			_ = s.Rest.DeleteWebhookMessage(webhook.ID(), webhook.Token, posted.ID, snowflakeID(threadID))
		}
		b.Logger.Error("failed to delete YouTube source link", zap.String("message", m.ID.String()), zap.Error(err))
		return false
	}
	return true
}

func (b *Bot) messageWebhook(s *bot.Client, channelID, name string) (*discord.IncomingWebhook, string, error) {
	channel, err := getDiscordChannel(s, channelID)
	if err != nil {
		return nil, "", err
	}
	threadID := ""
	if isThread(channel) {
		threadID, channelID = channelID, snowflakeString(channel.ParentID())
		if channelID == "" {
			return nil, "", fmt.Errorf("thread %s has no parent channel", threadID)
		}
	}
	b.webhookMu.Lock()
	defer b.webhookMu.Unlock()
	webhooks, err := s.Rest.GetWebhooks(snowflakeID(channelID))
	if err != nil {
		return nil, "", err
	}
	for _, webhook := range webhooks {
		incoming, ok := webhook.(discord.IncomingWebhook)
		if ok && incoming.Name() == name && incoming.Token != "" && (s.ID() == 0 || incoming.User.ID == s.ID()) {
			return &incoming, threadID, nil
		}
	}
	webhook, err := s.Rest.CreateWebhook(snowflakeID(channelID), discord.WebhookCreate{Name: name})
	return webhook, threadID, err
}
