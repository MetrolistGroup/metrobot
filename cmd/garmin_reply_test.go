package cmd

import (
	"reflect"
	"strings"
	"testing"
)

func TestGarminEmbedReplyRoundTrip(t *testing.T) {
	embed := &GarminEmbed{
		Title: "Release", Description: "a short answer", URL: "https://metrolist.cc", Color: 0xffffff,
		Fields: []GarminEmbedField{{Name: "Status", Value: "Available", Inline: true}},
	}
	wire := FormatGarminReply("here you go.", embed)
	content, got := ParseGarminReply(wire)
	if content != "here you go." || !reflect.DeepEqual(got, embed) {
		t.Fatalf("parsed reply = %q, %#v", content, got)
	}
	if got := GarminReplyPlainText(wire); got != "here you go.\nRelease\na short answer\nhttps://metrolist.cc\nStatus\nAvailable" {
		t.Fatalf("plain reply = %q", got)
	}
	content, got = ParseGarminReply(FormatGarminReply("", embed))
	if content != "" || got == nil {
		t.Fatal("embed-only reply was lost")
	}
}

func TestGarminEmbedLimits(t *testing.T) {
	for _, test := range []struct {
		name  string
		embed GarminEmbed
		valid bool
	}{
		{"title boundary", GarminEmbed{Title: strings.Repeat("a", 256)}, true},
		{"title overflow", GarminEmbed{Title: strings.Repeat("a", 257)}, false},
		{"unicode boundary", GarminEmbed{Title: strings.Repeat("😀", 128)}, true},
		{"unicode overflow", GarminEmbed{Title: strings.Repeat("😀", 129)}, false},
		{"description boundary", GarminEmbed{Description: strings.Repeat("a", 4096)}, true},
		{"description overflow", GarminEmbed{Description: strings.Repeat("a", 4097)}, false},
		{"field name boundary", GarminEmbed{Fields: []GarminEmbedField{{Name: strings.Repeat("a", 256), Value: "v"}}}, true},
		{"field name overflow", GarminEmbed{Fields: []GarminEmbedField{{Name: strings.Repeat("a", 257), Value: "v"}}}, false},
		{"field value boundary", GarminEmbed{Fields: []GarminEmbedField{{Name: "n", Value: strings.Repeat("a", 1024)}}}, true},
		{"field value overflow", GarminEmbed{Fields: []GarminEmbedField{{Name: "n", Value: strings.Repeat("a", 1025)}}}, false},
		{"blank field", GarminEmbed{Fields: []GarminEmbedField{{Name: "n", Value: " "}}}, false},
		{"total boundary", GarminEmbed{Description: strings.Repeat("a", 4096), Fields: []GarminEmbedField{{Name: "n", Value: strings.Repeat("a", 1024)}, {Name: "n", Value: strings.Repeat("b", 878)}}}, true},
		{"total overflow", GarminEmbed{Description: strings.Repeat("a", 4096), Fields: []GarminEmbedField{{Name: "n", Value: strings.Repeat("a", 1024)}, {Name: "n", Value: strings.Repeat("b", 879)}}}, false},
		{"black", GarminEmbed{Title: "t", Color: 0}, true},
		{"white", GarminEmbed{Title: "t", Color: 0xffffff}, true},
		{"negative color", GarminEmbed{Title: "t", Color: -1}, false},
		{"color overflow", GarminEmbed{Title: "t", Color: 0x1000000}, false},
		{"http", GarminEmbed{Title: "t", URL: "http://example.com"}, true},
		{"unsafe URL", GarminEmbed{Title: "t", URL: "javascript:alert(1)"}, false},
		{"relative URL", GarminEmbed{Title: "t", URL: "/release"}, false},
		{"missing host", GarminEmbed{Title: "t", URL: "https:///release"}, false},
		{"credentials", GarminEmbed{Title: "t", URL: "https://user:pass@example.com"}, false},
		{"blank", GarminEmbed{Title: " "}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, embed := ParseGarminReply(FormatGarminReply("answer", &test.embed))
			if (embed != nil) != test.valid || !strings.HasPrefix(content, "answer") {
				t.Fatalf("valid = %v, want %v, content %q", embed != nil, test.valid, content)
			}
			if !test.valid && strings.TrimSpace(test.embed.PlainText()) != "" && content == "answer" {
				t.Fatal("invalid embed did not degrade to text")
			}
		})
	}
	for _, count := range []int{25, 26} {
		e := &GarminEmbed{}
		for range count {
			e.Fields = append(e.Fields, GarminEmbedField{Name: "n", Value: "v"})
		}
		if _, embed := ParseGarminReply(FormatGarminReply("", e)); (embed != nil) != (count == 25) {
			t.Fatalf("field count %d accepted = %v", count, embed != nil)
		}
	}
}

func TestGarminEmbedRejectsMalformedBlocks(t *testing.T) {
	for _, input := range []string{
		"ordinary text", "```json\n{\"title\":\"t\"}\n```", "```embed\n{\"title\":\"t\"}",
		"```embed\n{}\n```", "```embed\nnull\n```",
		"```embed\n{\"title\":\"t\",\"color\":\"red\"}\n```", "```embed\n{\"title\":\"t\"}\n```\nextra",
		"```embed\n{\"title\":\"t\"}{}\n```", "inline ```embed\n{\"title\":\"t\"}\n```",
	} {
		if content, embed := ParseGarminReply(input); content != input || embed != nil {
			t.Errorf("malformed block changed: %q, %#v", content, embed)
		}
	}
}

func TestGarminEmbedAcceptsFooterAndIgnoresUnknownKeys(t *testing.T) {
	content, embed := ParseGarminReply("here you go\n```embed\n{\"title\":\"Test\",\"footer\":{\"text\":\"Metrolist\"},\"image\":{\"url\":\"https://example.com\"}}\n```")
	if content != "here you go" || embed == nil || embed.Title != "Test" || embed.Footer == nil || embed.Footer.Text != "Metrolist" {
		t.Fatalf("content %q, embed %#v", content, embed)
	}
}
