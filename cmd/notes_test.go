package cmd

import "testing"

func TestNormalizeNoteContent(t *testing.T) {
	input := "# Playback issues\\nUse high quality\r\nClear cache\\r\\nDone"
	want := "# Playback issues\nUse high quality\nClear cache\nDone"

	if got := normalizeNoteContent(input); got != want {
		t.Fatalf("normalizeNoteContent() = %q, want %q", got, want)
	}
}
