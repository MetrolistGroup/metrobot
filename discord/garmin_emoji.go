package discord

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
)

var (
	garminAICustomEmojiPattern     = regexp.MustCompile(`<a?:([A-Za-z0-9_~]+):(\d+)>`)
	garminAICustomShortcodePattern = regexp.MustCompile(`:([A-Za-z_~][A-Za-z0-9_~]{1,63}):`)
	garminAILooseShortcodePattern  = regexp.MustCompile(`(^|[[:space:]]):([A-Za-z_~][A-Za-z0-9_~]{1,63})([[:space:].,!?;]|$)`)
)

func garminAIEmojiByName(s *bot.Client, guildID, name string) (*discord.Emoji, bool) {
	emojis, err := garminLiveGuildEmojis(s, guildID)
	if err != nil {
		return nil, false
	}
	return garminEmojiByName(emojis, name)
}

func garminLiveGuildEmojis(s *bot.Client, guildID string) ([]discord.Emoji, error) {
	if s != nil && guildID != "" && s.Rest != nil {
		if emojis, err := s.Rest.GetEmojis(snowflakeID(guildID)); err == nil {
			return emojis, nil
		}
	}
	return garminGuildEmojis(s, guildID)
}

func garminGuildEmojis(s *bot.Client, guildID string) ([]discord.Emoji, error) {
	if s == nil || guildID == "" {
		return nil, fmt.Errorf("Discord guild emojis are unavailable")
	}
	if s.Caches != nil {
		if _, ok := s.Caches.Guild(snowflakeID(guildID)); ok {
			return slices.Collect(s.Caches.Emojis(snowflakeID(guildID))), nil
		}
	}
	if s.Rest == nil {
		return nil, fmt.Errorf("Discord guild emojis are unavailable")
	}
	return s.Rest.GetEmojis(snowflakeID(guildID))
}

func garminEmojiByName(emojis []discord.Emoji, name string) (*discord.Emoji, bool) {
	name = strings.Trim(strings.TrimSpace(name), ":")
	for _, emoji := range emojis {
		if emoji.ID != 0 && emoji.Available && strings.EqualFold(emoji.Name, name) {
			return &emoji, true
		}
	}
	return nil, false
}

func garminEmojiImageURL(emoji *discord.Emoji) string {
	extension := "png"
	if emoji.Animated {
		extension = "gif"
	}
	return fmt.Sprintf("https://cdn.discordapp.com/emojis/%s.%s?size=128&quality=lossless", emoji.ID, extension)
}

func renderGarminGuildEmojis(s *bot.Client, guildID, answer string) string {
	if !garminAICustomEmojiPattern.MatchString(answer) && !garminAICustomShortcodePattern.MatchString(answer) && !garminAILooseShortcodePattern.MatchString(answer) {
		return answer
	}
	emojis, err := garminLiveGuildEmojis(s, guildID)
	if err != nil {
		answer = garminAICustomEmojiPattern.ReplaceAllString(answer, "")
		answer = garminAICustomShortcodePattern.ReplaceAllString(answer, "")
		return strings.TrimSpace(answer)
	}

	placeholders := make([]string, 0)
	replaceEmoji := func(name string) string {
		emoji, ok := garminEmojiByName(emojis, name)
		if !ok {
			return ""
		}
		placeholder := fmt.Sprintf("\x00GARMIN_EMOJI_%d\x00", len(placeholders))
		placeholders = append(placeholders, emoji.Mention())
		return placeholder
	}
	answer = garminAICustomEmojiPattern.ReplaceAllStringFunc(answer, func(markup string) string {
		return replaceEmoji(garminAICustomEmojiPattern.FindStringSubmatch(markup)[1])
	})
	answer = garminAICustomShortcodePattern.ReplaceAllStringFunc(answer, func(shortcode string) string {
		return replaceEmoji(garminAICustomShortcodePattern.FindStringSubmatch(shortcode)[1])
	})
	answer = garminAILooseShortcodePattern.ReplaceAllStringFunc(answer, func(shortcode string) string {
		match := garminAILooseShortcodePattern.FindStringSubmatch(shortcode)
		if replacement := replaceEmoji(match[2]); replacement != "" {
			return match[1] + replacement + match[3]
		}
		return shortcode
	})
	for i, emoji := range placeholders {
		answer = strings.ReplaceAll(answer, fmt.Sprintf("\x00GARMIN_EMOJI_%d\x00", i), emoji)
	}
	for strings.Contains(answer, "  ") {
		answer = strings.ReplaceAll(answer, "  ", " ")
	}
	return strings.TrimSpace(answer)
}

var garminAIUnicodeReactions = map[string]struct{}{
	"\U0001F44D":   {},
	"\u2764\uFE0F": {},
	"\u2764":       {},
	"\U0001F602":   {},
	"\u2705":       {},
	"\u274C":       {},
	"\U0001F440":   {},
	"\U0001F389":   {},
	"\U0001F525":   {},
	"\U0001F480":   {},
}

func (b *Bot) listGarminGuildEmojis(s *bot.Client, guildID string) (string, error) {
	emojis, err := garminLiveGuildEmojis(s, guildID)
	if err != nil {
		return "", err
	}
	items := make([]map[string]any, 0, len(emojis))
	for _, emoji := range emojis {
		if emoji.ID == 0 || !emoji.Available {
			continue
		}
		items = append(items, map[string]any{
			"name":      emoji.Name,
			"shortcode": ":" + emoji.Name + ":",
			"animated":  emoji.Animated,
		})
	}
	return mustJSON(map[string]any{"emojis": items}), nil
}

func (b *Bot) viewGarminGuildEmoji(s *bot.Client, guildID, name string) (string, error) {
	emoji, ok := garminAIEmojiByName(s, guildID, name)
	if !ok {
		return "", fmt.Errorf("emoji %q is unavailable", name)
	}
	return mustJSON(map[string]any{
		"name":      emoji.Name,
		"shortcode": ":" + emoji.Name + ":",
		"animated":  emoji.Animated,
		"image_url": garminEmojiImageURL(emoji),
	}), nil
}

func addGarminReactions(s *bot.Client, m *discord.Message, reactions []string) (bool, error) {
	if s == nil || s.Rest == nil || m == nil {
		return false, fmt.Errorf("Discord message is unavailable")
	}
	added := false
	for _, reaction := range reactions {
		apiName := ""
		if _, allowed := garminAIUnicodeReactions[reaction]; allowed {
			apiName = reaction
		} else if emoji, ok := garminAIEmojiByName(s, snowflakeString(m.GuildID), strings.Trim(reaction, ":")); ok {
			apiName = emoji.Reaction()
		}
		if apiName == "" {
			if added {
				return true, nil
			}
			return false, fmt.Errorf("reaction %q is unavailable", reaction)
		}
		if err := s.Rest.AddReaction(m.ChannelID, m.ID, apiName); err != nil {
			if added {
				return true, nil
			}
			return false, fmt.Errorf("adding reaction: %w", err)
		}
		added = true
	}
	return added, nil
}
