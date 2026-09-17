package discord

import (
	"bytes"
	"image"
	"image/color"
	_ "image/png"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestQuoteUsesServerDisplayNames(t *testing.T) {
	session, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild"}); err != nil {
		t.Fatal(err)
	}
	author := &discordgo.User{ID: "123456789012345678", Username: "author-account", GlobalName: "Author Global"}
	mentioned := &discordgo.User{ID: "234567890123456789", Username: "mentioned-account", GlobalName: "Mention Global"}
	replied := &discordgo.User{ID: "345678901234567890", Username: "replied-account", GlobalName: "Replied Global"}
	for _, member := range []*discordgo.Member{
		{GuildID: "guild", Nick: "Author Server", User: author},
		{GuildID: "guild", Nick: "Mention Server", User: mentioned},
		{GuildID: "guild", Nick: "Replied Server", User: replied},
	} {
		if err := session.State.MemberAdd(member); err != nil {
			t.Fatal(err)
		}
	}
	message := &discordgo.Message{
		GuildID: "guild", Author: author, Mentions: []*discordgo.User{mentioned},
		Content:           "hello <@234567890123456789> and <@!234567890123456789>",
		MessageReference:  &discordgo.MessageReference{MessageID: "reply"},
		ReferencedMessage: &discordgo.Message{ID: "reply", Author: replied},
	}

	if name, _ := quoteAuthor(session, message); name != "Author Server" {
		t.Fatalf("quote author = %q", name)
	}
	if got := resolveQuoteMentions(session, message, message.Content); got != "hello @Mention Server and @Mention Server" {
		t.Fatalf("quote text = %q", got)
	}
	if got := quoteReplyName(session, message); got != "@Replied Server" {
		t.Fatalf("quote reply name = %q", got)
	}

	outsider := &discordgo.User{ID: "456789012345678901", Username: "account-name", GlobalName: "Global Name"}
	name, _ := quoteAuthor(session, &discordgo.Message{GuildID: "guild", Author: outsider})
	if name != "account-name" {
		t.Fatalf("quote fallback author = %q", name)
	}
}

func TestQuoteMarkdownStylesAndNewlines(t *testing.T) {
	spans := parseQuoteMarkdown("**bold** and *italic*\n\n__under__ `code` ~~gone~~ [site](https://example.com) ||hidden||")
	var plain strings.Builder
	styles := make(map[string]quoteTextStyle)
	for _, span := range spans {
		plain.WriteString(span.text)
		styles[span.text] |= span.style
	}
	if got := plain.String(); got != "bold and italic\n\nunder code gone site hidden" {
		t.Fatalf("rendered markdown text = %q", got)
	}
	for text, style := range map[string]quoteTextStyle{
		"bold": quoteBold, "italic": quoteItalic, "under": quoteUnderline,
		"code": quoteCode, "gone": quoteStrike, "site": quoteLink | quoteUnderline,
		"hidden": quoteSpoiler,
	} {
		if styles[text]&style != style {
			t.Errorf("style for %q = %08b, want %08b", text, styles[text], style)
		}
	}

	faces, err := newQuoteFaceSet(32)
	if err != nil {
		t.Fatal(err)
	}
	defer faces.Close()
	lines := wrapQuoteText(faces, parseQuoteMarkdown("first\n\nsecond"), quoteTextWidth)
	if len(lines) != 3 || len(lines[1]) != 0 {
		t.Fatalf("explicit newlines produced %#v", lines)
	}
}

func TestQuoteTriggersAndImageOutput(t *testing.T) {
	for _, trigger := range []string{"ogc", " OGC ", "garmin clip that", "ok garmin video speichern", "garmin quote"} {
		if !isQuoteTrigger(trigger) {
			t.Errorf("isQuoteTrigger(%q) = false", trigger)
		}
	}
	for _, other := range []string{"ogc now", "quote", "garmin clip this"} {
		if isQuoteTrigger(other) {
			t.Errorf("isQuoteTrigger(%q) = true", other)
		}
	}

	avatar := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			avatar.SetRGBA(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 90, A: 255})
		}
	}
	data, err := renderQuote(strings.Repeat("a quoted message ", 80), "Quote Author", "@Original Author", avatar)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || cfg.Width != quoteWidth || cfg.Height != quoteHeight {
		t.Fatalf("quote image = %s %dx%d", format, cfg.Width, cfg.Height)
	}
}
