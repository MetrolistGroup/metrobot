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
		{name: "plain hashtag", content: "#playback", want: "playback"},
		{name: "with trailing text", content: "#playback please", want: "playback"},
		{name: "invalid leading space", content: "# playback", want: ""},
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
	if got := extractTriggeredNoteName("#playback@metrolist_robot", "metrolist_robot"); got != "playback" {
		t.Fatalf("extractTriggeredNoteName() = %q, want %q", got, "playback")
	}

	if got := extractTriggeredNoteName("#playback@another_bot", "metrolist_robot"); got != "playback@another_bot" {
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
