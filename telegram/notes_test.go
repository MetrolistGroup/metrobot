package telegram

import (
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/internal/notefmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestTelegramFAQNoteUsesInlineKeyboard(t *testing.T) {
	document, err := notefmt.Parse(notefmt.Fence("faq", "# Download\n\n## Where?\nhttps://metrolist.cc\n\n## How *now*?\nTap `Download`."))
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := newTelegramNoteKeyboardMessage(1, "download", document.Render())
	if !ok {
		t.Fatal("FAQ note did not use keyboard delivery")
	}
	markup := msg.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	if len(markup.InlineKeyboard) != 2 || markup.InlineKeyboard[1][0].Text != "How now?" {
		t.Fatalf("keyboard = %#v", markup.InlineKeyboard)
	}
	for index, row := range markup.InlineKeyboard {
		if data := *row[0].CallbackData; len(data) > 64 || data != faqCallbackData("Download", index) {
			t.Fatalf("callback data %q", data)
		}
	}
	if strings.Contains(msg.Text, "Tap") {
		t.Fatal("initial FAQ message leaked an answer")
	}
}

func TestTelegramPlainNoteKeepsTextPath(t *testing.T) {
	document, err := notefmt.Parse("Plain **note**")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := newTelegramNoteKeyboardMessage(1, "plain", document.Render()); ok {
		t.Fatal("plain note used keyboard delivery")
	}
}
