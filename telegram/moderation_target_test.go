package telegram

import (
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/util"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func textMention(text, name string, id int64) tgbotapi.MessageEntity {
	i := strings.Index(text, name)
	return tgbotapi.MessageEntity{Type: "text_mention", Offset: util.UTF16Len(text[:i]), Length: util.UTF16Len(name), User: &tgbotapi.User{ID: id}}
}

func TestExtractTelegramUserIDIgnoresReasonMentions(t *testing.T) {
	command := tgbotapi.MessageEntity{Type: "bot_command", Offset: 0, Length: 4}
	text := "/ban @spammer harassing Victim"
	msg := &tgbotapi.Message{Text: text, Entities: []tgbotapi.MessageEntity{
		command,
		{Type: "mention", Offset: 5, Length: 8},
		textMention(text, "Victim", 999),
	}}
	if got := extractTelegramUserID(msg, "@spammer"); got != "" {
		t.Fatalf("unresolved @spammer resolved to %q", got)
	}

	// Emoji take two UTF-16 units, so byte or rune offsets would miss the entity.
	text = "/warn 😀Spammer for spamming 🎉 at Victim"
	msg = &tgbotapi.Message{Text: text, Entities: []tgbotapi.MessageEntity{
		{Type: "bot_command", Offset: 0, Length: 5},
		textMention(text, "😀Spammer", 111),
		textMention(text, "Victim", 999),
	}}
	if got := extractTelegramUserID(msg, "😀Spammer"); got != "111" {
		t.Fatalf("target = %q, want 111", got)
	}

	// "😀x " is two UTF-16 units for the emoji plus two more, while its byte
	// length is 6 and its rune count is 3.
	if offset, ok := telegramArgumentOffset("/ban 😀x Victim", "Victim"); !ok || offset != 9 {
		t.Fatalf("UTF-16 offset = %d, %v; want 9", offset, ok)
	}

	// The same display name later in the reason must not win.
	text = "/warn Alex promoted by Alex"
	first := textMention(text, "Alex", 222)
	second := first
	second.Offset = util.UTF16Len("/warn Alex promoted by ")
	second.User = &tgbotapi.User{ID: 333}
	msg = &tgbotapi.Message{Text: text, Entities: []tgbotapi.MessageEntity{second, first}}
	if got := extractTelegramUserID(msg, "Alex"); got != "222" {
		t.Fatalf("target = %q, want 222", got)
	}

	msg = &tgbotapi.Message{Text: "/ban 12345 reason mentioning Victim", Entities: []tgbotapi.MessageEntity{textMention("/ban 12345 reason mentioning Victim", "Victim", 999)}}
	if got := extractTelegramUserID(msg, "12345"); got != "12345" {
		t.Fatalf("numeric target = %q", got)
	}

	msg.ReplyToMessage = &tgbotapi.Message{From: &tgbotapi.User{ID: 777}}
	if got := extractTelegramUserID(msg, ""); got != "777" {
		t.Fatalf("reply target = %q, want 777", got)
	}
	msg.ReplyToMessage.SenderChat = &tgbotapi.Chat{ID: testChatID}
	if got := extractTelegramUserID(msg, "12345"); got != "" {
		t.Fatalf("anonymous reply target = %q, want unresolved", got)
	}
	msg.ReplyToMessage = &tgbotapi.Message{}
	if got := extractTelegramUserID(msg, "12345"); got != "" {
		t.Fatalf("reply without sender fell back to reason token %q", got)
	}
	msg.ReplyToMessage = nil
	for _, invalid := range []string{"0", "-123", "@0", "@-123"} {
		if got := extractTelegramUserID(msg, invalid); got != "" {
			t.Errorf("invalid target %q resolved to %q", invalid, got)
		}
	}
}

func TestChatModPatternRequiresCommandBoundary(t *testing.T) {
	for _, content := range []string{"!warnings", "!bananas", "!banana split", "!muted"} {
		if m := chatModPattern.FindStringSubmatch(content); m != nil {
			t.Errorf("%q matched as %q", content, m[1])
		}
	}
	for content, want := range map[string][2]string{
		"!warn":             {"warn", ""},
		"!ban @user spam":   {"ban", "@user spam"},
		"!TBAN 123 1h spam": {"tban", "123 1h spam"},
	} {
		m := chatModPattern.FindStringSubmatch(content)
		if m == nil || !strings.EqualFold(m[1], want[0]) || m[2] != want[1] {
			t.Errorf("%q = %q, want %q", content, m, want)
		}
	}
}
