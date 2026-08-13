package discord

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestGarminAIFooter(t *testing.T) {
	want := "\n\n-# This response was generated using Upstage Solar Pro 4 via OpenRouter"
	if got := garminAIFooter("Upstage Solar Pro 4 via OpenRouter"); got != want {
		t.Fatalf("garminAIFooter() = %q, want %q", got, want)
	}
}

func TestTruncateGarminAIResponse(t *testing.T) {
	footer := garminAIFooter("Upstage Solar Pro 4 via OpenRouter")
	input := strings.Repeat("a", garminAIMaxContent)
	got := truncateGarminAIResponse(input, footer)
	if len(got)+len(footer) > garminAIMaxContent {
		t.Fatalf("response length = %d, max %d", len(got)+len(footer), garminAIMaxContent)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated response = %q, want ellipsis", got)
	}
}

func TestTruncateGarminAIResponsePreservesUTF8(t *testing.T) {
	footer := garminAIFooter("Deepseek v4 Flash")
	input := strings.Repeat("é", garminAIMaxContent)
	got := truncateGarminAIResponse(input, footer)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated response is invalid UTF-8: %q", got)
	}
}
