package discord

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateGarminAIResponse(t *testing.T) {
	input := strings.Repeat("a", garminAIMaxContent)
	got := truncateGarminAIResponse(input)
	if len(got)+len(garminAILabel) > garminAIMaxContent {
		t.Fatalf("response length = %d, max %d", len(got)+len(garminAILabel), garminAIMaxContent)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated response = %q, want ellipsis", got)
	}
}

func TestTruncateGarminAIResponsePreservesUTF8(t *testing.T) {
	input := strings.Repeat("é", garminAIMaxContent)
	got := truncateGarminAIResponse(input)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated response is invalid UTF-8: %q", got)
	}
}
