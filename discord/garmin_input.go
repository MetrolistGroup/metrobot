package discord

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
)

func (b *Bot) garminAIUserMessage(m *discord.Message, prompt string) cmd.GarminAIMessage {
	message := garminAIUserMessage(m, prompt)
	if !b.garminAIReferenceVisible(m) {
		return message
	}
	reference := m.ReferencedMessage
	referenceImages := append(garminAIImageURLs(reference), garminAIEmojiImageURLs(reference)...)
	message.Images = uniqueGarminAIImageURLs(append(message.Images, referenceImages...), garminAIMaxImages)
	bounded := []cmd.GarminAIMessage{message}
	boundGarminEmojiImages(bounded)
	return bounded[0]
}

func (b *Bot) garminAIReferenceVisible(m *discord.Message) bool {
	return m != nil && m.ReferencedMessage != nil && garminAIReferenceInScope(m) &&
		(b == nil || b.garminMessageVisible(m.ChannelID.String(), m.ReferencedMessage.ID.String()))
}

func garminAIReferenceInScope(m *discord.Message) bool {
	if m == nil {
		return false
	}
	if reference := m.MessageReference; reference != nil && reference.ChannelID != nil && *reference.ChannelID != m.ChannelID {
		return false
	}
	if reference := m.ReferencedMessage; reference != nil {
		if reference.ChannelID != 0 && reference.ChannelID != m.ChannelID {
			return false
		}
		if reference.GuildID != nil && snowflakeString(reference.GuildID) != snowflakeString(m.GuildID) {
			return false
		}
	}
	return true
}

func garminAIUserMessage(m *discord.Message, prompt string) cmd.GarminAIMessage {
	name := ""
	if m != nil && m.Author.ID != 0 {
		name = garminAIMessageName(m.Author.ID.String())
	}
	message := cmd.GarminAIMessage{
		Role:    "user",
		Name:    name,
		Content: strings.TrimSpace(prompt),
		Images:  uniqueGarminAIImageURLs(append(garminAIImageURLs(m), garminAIEmojiImageURLs(m)...), garminAIMaxImages),
	}
	if message.Content == "" && len(message.Images) > 0 {
		message.Content = "what is in this image?"
	}
	if m != nil {
		message.Timestamp = m.CreatedAt.UTC()
	}
	return message
}

func garminAIMessageName(userID string) string {
	if userID == "" {
		return ""
	}
	return "discord_" + userID
}

func garminAIMessageHasInput(message cmd.GarminAIMessage) bool {
	return strings.TrimSpace(message.Content) != "" || len(message.Images) > 0
}

func garminAIImageURLs(m *discord.Message) []string {
	if m == nil {
		return nil
	}
	var images []string
	for _, attachment := range m.Attachments {
		if !garminAIImageAttachment(&attachment) {
			continue
		}
		imageURL := strings.TrimSpace(attachment.URL)
		parsed, err := url.Parse(imageURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			continue
		}
		images = append(images, imageURL)
		if len(images) == garminAIMaxImages {
			break
		}
	}
	return images
}

func garminAIImageAttachment(attachment *discord.Attachment) bool {
	contentType := strings.ToLower(strings.TrimSpace(value(attachment.ContentType)))
	if strings.HasPrefix(contentType, "image/") && contentType != "image/svg+xml" {
		return true
	}
	switch strings.ToLower(filepath.Ext(attachment.Filename)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	default:
		return false
	}
}

func garminAIToolImageURLs(toolName, output string) []string {
	if toolName != "read_community_channel" && toolName != "view_discord_emoji" {
		return nil
	}
	var result struct {
		ImageURL string `json:"image_url"`
		Messages []struct {
			Attachments []struct {
				Filename    string `json:"filename"`
				ContentType string `json:"content_type"`
				URL         string `json:"url"`
			} `json:"attachments"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil
	}
	var images []string
	if strings.HasPrefix(strings.TrimSpace(result.ImageURL), "https://") {
		images = append(images, result.ImageURL)
	}
	for _, message := range result.Messages {
		for _, attachment := range message.Attachments {
			if !garminAIImageAttachment(&discord.Attachment{
				Filename: attachment.Filename, ContentType: ptr(attachment.ContentType),
			}) || !strings.HasPrefix(strings.TrimSpace(attachment.URL), "https://") {
				continue
			}
			images = append(images, strings.TrimSpace(attachment.URL))
		}
	}
	return uniqueGarminAIImageURLs(images, garminAIMaxImages)
}

func uniqueGarminAIImageURLs(images []string, limit int) []string {
	if len(images) == 0 || limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(images))
	unique := make([]string, 0, min(len(images), limit))
	for _, imageURL := range images {
		imageURL = strings.TrimSpace(imageURL)
		if imageURL == "" {
			continue
		}
		if _, exists := seen[imageURL]; exists {
			continue
		}
		seen[imageURL] = struct{}{}
		unique = append(unique, imageURL)
		if len(unique) == limit {
			break
		}
	}
	return unique
}
