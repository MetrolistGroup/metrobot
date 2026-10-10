package discord

import (
	"bytes"
	"image"
	"image/color"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestQuoteUsesServerDisplayNames(t *testing.T) {
	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: snowflakeID("100000000000000012")}}})

	author := &discord.User{ID: snowflakeID("123456789012345678"), Username: "author-account", GlobalName: ptr("Author Global")}
	mentioned := &discord.User{ID: snowflakeID("234567890123456789"), Username: "mentioned-account", GlobalName: ptr("Mention Global")}
	replied := &discord.User{ID: snowflakeID("345678901234567890"), Username: "replied-account", GlobalName: ptr("Replied Global")}
	for _, member := range []*discord.Member{{GuildID: snowflakeID("100000000000000012"), Nick: ptr("Author Server"), User: *author}, {GuildID: snowflakeID("100000000000000012"), Nick: ptr("Mention Server"), User: *mentioned}, {GuildID: snowflakeID("100000000000000012"), Nick: ptr("Replied Server"), User: *replied}} {
		session.Caches.AddMember(*member)

	}
	message := &discord.Message{
		GuildID: snowflakePtr("100000000000000012"), Author: *author, Mentions: []discord.User{*mentioned},
		Content: "hello <@234567890123456789> and <@!234567890123456789>", MessageReference: &discord.MessageReference{MessageID: snowflakePtr("100000000000000038")},
		ReferencedMessage: &discord.Message{ID: snowflakeID("100000000000000038"), Author: *replied},
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

	outsider := &discord.User{ID: snowflakeID("456789012345678901"), Username: "account-name", GlobalName: ptr("Global Name")}
	name, _ := quoteAuthor(session, &discord.Message{GuildID: snowflakePtr("100000000000000012"), Author: *outsider})
	if name != "account-name" {
		t.Fatalf("quote fallback author = %q", name)
	}
}

func TestQuoteMarkdownStylesAndNewlines(t *testing.T) {
	spans := parseQuoteMarkdown("**bold** and *italic*\n\n__under__ `code` ~~gone~~ [site](https://example.com) ||hidden `secret`||")
	var plain strings.Builder
	styles := make(map[string]quoteTextStyle)
	for _, span := range spans {
		plain.WriteString(span.text)
		styles[span.text] |= span.style
	}
	if got := plain.String(); got != "bold and italic\n\nunder code gone site hidden secret" {
		t.Fatalf("rendered markdown text = %q", got)
	}
	for text, style := range map[string]quoteTextStyle{
		"bold": quoteBold, "italic": quoteItalic, "under": quoteUnderline,
		"code": quoteCode, "gone": quoteStrike, "site": quoteLink | quoteUnderline,
		"hidden ": quoteSpoiler, "secret": quoteCode | quoteSpoiler,
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

func TestQuoteMessageCommandClipsSelectedMessage(t *testing.T) {
	deferred, clipped := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/interactions/100000000000000017/token/callback"):
			deferred = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && (strings.Contains(r.URL.Path, "/embed/avatars/") || strings.Contains(r.URL.Path, "/guilds/100000000000000012/members/")):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/webhooks/100000000000000001/token/messages/@original"):
			clipped = strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"100000000000000037"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Rest = testREST(server.Client())
	session.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: session.Rest.HTTPClient().Transport, target: server.URL}
	bot := &Bot{Logger: zap.NewNop()}
	interaction := testCommandInteraction(t, session, `{
		"id":"100000000000000017","application_id":"100000000000000001","token":"token","type":2,
		"guild_id":"100000000000000012","channel":{"id":"100000000000000005","type":0},
		"data":{"name":"Clip","type":3,"target_id":"100000000000000045","resolved":{"messages":{
			"100000000000000045":{"id":"100000000000000045","content":"selected message","author":{"id":"123456789012345678","username":"author"}}
		}}}
	}`)
	bot.handleQuoteInteraction(session, interaction)

	if !deferred || !clipped {
		t.Fatalf("message command deferred=%v, clipped selected message=%v", deferred, clipped)
	}
}

func TestQuoteTriggersAndImageOutput(t *testing.T) {
	for _, trigger := range []string{"ogc", " OGC ", "garmin clip that", "garmin clip this", "ok garmin video speichern", "garmin quote"} {
		if !isQuoteTrigger(trigger) {
			t.Errorf("isQuoteTrigger(%q) = false", trigger)
		}
	}
	for _, other := range []string{"ogc now", "quote", "garmin clip these"} {
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
