package discord

import "testing"

func TestSuppressDiscordEmbedsWrapsBareURLs(t *testing.T) {
	input := "compare: https://example.com/release"
	want := "compare: <https://example.com/release>"

	if got := suppressDiscordEmbeds(input); got != want {
		t.Fatalf("suppressDiscordEmbeds() = %q, want %q", got, want)
	}
}

func TestSuppressDiscordEmbedsKeepsWrappedURLs(t *testing.T) {
	input := "download: <https://example.com/release>"
	want := input

	if got := suppressDiscordEmbeds(input); got != want {
		t.Fatalf("suppressDiscordEmbeds() = %q, want %q", got, want)
	}
}

func TestSuppressDiscordEmbedsSkipsCodeFences(t *testing.T) {
	input := "```\nhttps://example.com/in-code\n```\noutside https://example.com/outside"
	want := "```\nhttps://example.com/in-code\n```\noutside <https://example.com/outside>"

	if got := suppressDiscordEmbeds(input); got != want {
		t.Fatalf("suppressDiscordEmbeds() = %q, want %q", got, want)
	}
}
