package telegram

import (
	"strings"
	"testing"
)

func TestExtractTriggeredNoteName(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "plain dot prefix", content: ".playback", want: "playback"},
		{name: "with trailing text", content: ".playback please", want: "playback"},
		{name: "invalid leading space", content: ". playback", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractTriggeredNoteName(tt.content, "metrolist_robot"); got != tt.want {
				t.Fatalf("extractTriggeredNoteName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTriggeredNoteNameMentionSuffix(t *testing.T) {
	if got := extractTriggeredNoteName(".playback@metrolist_robot", "metrolist_robot"); got != "playback" {
		t.Fatalf("extractTriggeredNoteName() = %q, want %q", got, "playback")
	}

	if got := extractTriggeredNoteName(".playback@another_bot", "metrolist_robot"); got != "playback@another_bot" {
		t.Fatalf("extractTriggeredNoteName() = %q, want %q", got, "playback@another_bot")
	}
}

func TestFormatTelegramNoteHTML(t *testing.T) {
	input := "📝 **Available notes:**\n• `playback`\n\n# Playback issues\n- Remove `VISITOR DATA` row"
	formatted := formatTelegramNoteHTML(input)

	expectedParts := []string{
		"📝 <b>Available notes:</b>",
		"• <code>playback</code>",
		"<b>Playback issues</b>",
		"• Remove <code>VISITOR DATA</code> row",
	}

	for _, part := range expectedParts {
		if !strings.Contains(formatted, part) {
			t.Fatalf("formatted note missing %q in %q", part, formatted)
		}
	}
}

func TestFormatTelegramNoteHTMLKeepsBoldOutOfCode(t *testing.T) {
	formatted := formatTelegramNoteHTML("`**do not bold**`")
	want := "<code>**do not bold**</code>"
	if formatted != want {
		t.Fatalf("formatTelegramNoteHTML() = %q, want %q", formatted, want)
	}
}

func TestFormatTelegramNoteHTMLSuppressesWrappedURLAutolinks(t *testing.T) {
	formatted := formatTelegramNoteHTML("No preview <https://i.imgur.com/9zgCCeX.png>")
	want := "No preview &lt;<code>https://i.imgur.com/9zgCCeX.png</code>&gt;"
	if formatted != want {
		t.Fatalf("formatTelegramNoteHTML() = %q, want %q", formatted, want)
	}
}

func TestHasEmbeddableDiscordURL(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "raw url", text: "Preview https://example.com/image.png", want: true},
		{name: "wrapped url", text: "No preview <https://example.com/image.png>", want: false},
		{name: "mixed urls", text: "No preview <https://example.com/a.png> preview https://example.com/b.png", want: true},
		{name: "no urls", text: "No preview here", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasEmbeddableDiscordURL(tt.text); got != tt.want {
				t.Fatalf("hasEmbeddableDiscordURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewTelegramNoteMessagePreviewFlag(t *testing.T) {
	raw := newTelegramNoteMessage(123, "Preview https://example.com/image.png")
	if raw.DisableWebPagePreview {
		t.Fatalf("raw URL note disabled web page preview")
	}

	wrapped := newTelegramNoteMessage(123, "No preview <https://example.com/image.png>")
	if !wrapped.DisableWebPagePreview {
		t.Fatalf("wrapped URL note allowed web page preview")
	}
}

func TestTimedModerationArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		isReply      bool
		wantDuration string
		wantReason   string
		wantOK       bool
	}{
		{name: "normal command", args: []string{"123", "1h", "spam"}, wantDuration: "1h", wantReason: "spam", wantOK: true},
		{name: "reply command", args: []string{"1h", "spam"}, isReply: true, wantDuration: "1h", wantReason: "spam", wantOK: true},
		{name: "normal missing reason", args: []string{"123", "1h"}, wantOK: false},
		{name: "reply missing reason", args: []string{"1h"}, isReply: true, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDuration, gotReason, gotOK := timedModerationArgs(tt.args, tt.isReply)
			if gotOK != tt.wantOK || gotDuration != tt.wantDuration || gotReason != tt.wantReason {
				t.Fatalf("timedModerationArgs() = (%q, %q, %v), want (%q, %q, %v)", gotDuration, gotReason, gotOK, tt.wantDuration, tt.wantReason, tt.wantOK)
			}
		})
	}
}
