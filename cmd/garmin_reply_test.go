package cmd

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGarminEmbedReplyRoundTrip(t *testing.T) {
	embed := &GarminEmbed{
		Title: "Release", Description: "a short answer", URL: "https://metrolist.cc", Color: 0xffffff,
		Fields:    []GarminEmbedField{{Name: "Status", Value: "Available", Inline: true}},
		Author:    &GarminEmbedAuthor{Name: "Metrolist", URL: "http://metrolist.cc/team", IconURL: "https://metrolist.cc/author.png"},
		Footer:    &GarminEmbedFooter{Text: "Footer", IconURL: "https://metrolist.cc/footer.png"},
		Thumbnail: &GarminEmbedResource{URL: "https://metrolist.cc/thumb.png"},
		Image:     &GarminEmbedResource{URL: "https://metrolist.cc/image.png"},
		Timestamp: "2026-11-03T12:00:00.123Z",
		Buttons:   []GarminLinkButton{{Label: "Open", URL: "https://metrolist.cc"}},
	}
	wire := FormatGarminReply("here you go.", embed)
	content, got := ParseGarminReply(wire)
	if content != "here you go." || !reflect.DeepEqual(got, embed) {
		t.Fatalf("parsed reply = %q, %#v", content, got)
	}
	if got := GarminReplyPlainText(wire); got != "here you go.\nMetrolist\nhttp://metrolist.cc/team\nRelease\na short answer\nhttps://metrolist.cc\nStatus\nAvailable\nFooter\nhttps://metrolist.cc/thumb.png\nhttps://metrolist.cc/image.png\nOpen: https://metrolist.cc" {
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
		{"author and footer total boundary", GarminEmbed{Description: strings.Repeat("a", 4096), Author: &GarminEmbedAuthor{Name: strings.Repeat("a", 256)}, Footer: &GarminEmbedFooter{Text: strings.Repeat("a", 1648)}}, true},
		{"author and footer total overflow", GarminEmbed{Description: strings.Repeat("a", 4096), Author: &GarminEmbedAuthor{Name: strings.Repeat("a", 256)}, Footer: &GarminEmbedFooter{Text: strings.Repeat("a", 1649)}}, false},
		{"http", GarminEmbed{Title: "t", URL: "http://example.com"}, true},
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
		"```embed\n{\"title\":\"t\"}\n```\nextra",
		"```embed\n{\"title\":\"t\"}{}\n```", "inline ```embed\n{\"title\":\"t\"}\n```",
	} {
		if content, embed := ParseGarminReply(input); content != input || embed != nil {
			t.Errorf("malformed block changed: %q, %#v", content, embed)
		}
	}
}

func TestGarminEmbedAcceptsFooterAndIgnoresUnknownKeys(t *testing.T) {
	content, embed := ParseGarminReply("here you go\n```embed\n{\"title\":\"Test\",\"footer\":{\"text\":\"Metrolist\"},\"unknown\":{\"ignored\":true}}\n```")
	if content != "here you go" || embed == nil || embed.Title != "Test" || embed.Footer == nil || embed.Footer.Text != "Metrolist" {
		t.Fatalf("content %q, embed %#v", content, embed)
	}
}

func TestGarminEmbedParsedBlocksNeverLeakJSON(t *testing.T) {
	for _, data := range []string{"{}", "null", "[]", `{"unknown":true}`, `{"title":"t","color":"red"}`, `{"image":{"url":42}}`, `{"buttons":[{"label":"bad","custom_id":"click"}]}`} {
		t.Run(data, func(t *testing.T) {
			content, embed := ParseGarminReply("answer\n```embed\n" + data + "\n```")
			if embed != nil || (content != "answer" && content != "answer\nt") {
				t.Fatalf("parsed block leaked or survived: %q, %#v", content, embed)
			}
		})
	}
	if content, embed := ParseGarminReply("```embed\n{}\n```"); content != "" || embed != nil {
		t.Fatalf("empty block = %q, %#v", content, embed)
	}
}

func TestGarminEmbedOptionalLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		part GarminEmbed
		keep bool
	}{
		{"author boundary", GarminEmbed{Author: &GarminEmbedAuthor{Name: strings.Repeat("😀", 128)}}, true},
		{"author overflow", GarminEmbed{Author: &GarminEmbedAuthor{Name: strings.Repeat("😀", 129)}}, false},
		{"blank author", GarminEmbed{Author: &GarminEmbedAuthor{Name: " "}}, false},
		{"footer boundary", GarminEmbed{Footer: &GarminEmbedFooter{Text: strings.Repeat("😀", 1024)}}, true},
		{"footer overflow", GarminEmbed{Footer: &GarminEmbedFooter{Text: strings.Repeat("😀", 1025)}}, false},
		{"blank footer", GarminEmbed{Footer: &GarminEmbedFooter{IconURL: "https://example.com/icon.png"}}, false},
		{"label boundary", GarminEmbed{Buttons: []GarminLinkButton{{Label: strings.Repeat("😀", 40), URL: "http://example.com"}}}, true},
		{"label overflow", GarminEmbed{Buttons: []GarminLinkButton{{Label: strings.Repeat("😀", 41), URL: "https://example.com"}}}, false},
		{"blank label", GarminEmbed{Buttons: []GarminLinkButton{{Label: " ", URL: "https://example.com"}}}, false},
		{"timestamp", GarminEmbed{Timestamp: "2026-11-03T12:00:00+02:00"}, true},
		{"bad timestamp", GarminEmbed{Timestamp: "yesterday"}, false},
		{"timestamp without zone", GarminEmbed{Timestamp: "2026-11-03T12:00:00"}, false},
		{"negative color", GarminEmbed{Color: -1}, false},
		{"color overflow", GarminEmbed{Color: 0x1000000}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.part.Title = "t"
			content, embed := ParseGarminReply(FormatGarminReply("answer", &test.part))
			if content != "answer" || embed == nil {
				t.Fatalf("invalid part discarded the reply: %q, %#v", content, embed)
			}
			if test.keep {
				if !reflect.DeepEqual(*embed, test.part) {
					t.Fatalf("valid part lost: %#v", embed)
				}
			} else if embed.Author != nil || embed.Footer != nil || len(embed.Buttons) != 0 || embed.Timestamp != "" || embed.Color != 0 {
				t.Fatalf("invalid part survived: %#v", embed)
			}
		})
	}
}

func TestGarminEmbedURLs(t *testing.T) {
	for _, test := range []struct {
		name, url   string
		link, image bool
	}{
		{"https", "https://example.com/image.png", true, true},
		{"http", "http://example.com/image.png", true, false},
		{"length boundary", "https://example.com/" + strings.Repeat("a", 2028), true, true},
		{"length overflow", "https://example.com/" + strings.Repeat("a", 2029), false, false},
		{"script", "javascript:alert(1)", false, false},
		{"relative", "/image.png", false, false},
		{"no host", "https:///image.png", false, false},
		{"credentials", "https://user:pass@example.com", false, false},
		{"empty credentials", "https://@example.com", false, false},
		{"space", "https://example.com/a b", false, false},
		{"newline", "https://example.com/a\nb", false, false},
		{"bad escape", "https://example.com/%xx", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := &GarminEmbed{Title: "t", URL: test.url,
				Author:    &GarminEmbedAuthor{Name: "author", URL: test.url, IconURL: test.url},
				Footer:    &GarminEmbedFooter{Text: "footer", IconURL: test.url},
				Thumbnail: &GarminEmbedResource{URL: test.url}, Image: &GarminEmbedResource{URL: test.url},
				Buttons: []GarminLinkButton{{Label: "Open", URL: test.url}},
			}
			_, embed := ParseGarminReply(FormatGarminReply("", input))
			if embed == nil || embed.Author == nil || embed.Footer == nil {
				t.Fatalf("URL discarded valid text: %#v", embed)
			}
			if (embed.URL == test.url) != test.link || (embed.Author.URL == test.url) != test.link || (len(embed.Buttons) == 1) != test.link ||
				(embed.Author.IconURL == test.url) != test.image || (embed.Footer.IconURL == test.url) != test.image || (embed.Image != nil) != test.image || (embed.Thumbnail != nil) != test.image {
				t.Fatalf("URL rules not applied: %#v", embed)
			}
		})
	}
}

func TestGarminEmbedStandalonePartsAndNow(t *testing.T) {
	for _, part := range []*GarminEmbed{
		{Author: &GarminEmbedAuthor{Name: "author"}}, {Footer: &GarminEmbedFooter{Text: "footer"}},
		{Image: &GarminEmbedResource{URL: "https://example.com/image.png"}},
		{Thumbnail: &GarminEmbedResource{URL: "https://example.com/thumb.png"}},
		{Timestamp: "now"},
	} {
		before := time.Now().UTC().Truncate(time.Second)
		_, embed := ParseGarminReply(FormatGarminReply("", part))
		if embed == nil || !embed.HasEmbedContent() {
			t.Fatalf("standalone part lost: %#v", part)
		}
		if part.Timestamp == "now" {
			now, err := time.Parse(time.RFC3339, embed.Timestamp)
			if err != nil || now.Before(before) || now.After(time.Now()) || now.Location() != time.UTC {
				t.Fatalf("now = %q, %v", embed.Timestamp, err)
			}
		}
	}
}

func TestGarminReplyLinkButtons(t *testing.T) {
	content, embed := ParseGarminReply("```embed\n{\"buttons\":[{\"label\":\"ignored\",\"custom_id\":\"click\"},{\"label\":\"Open\",\"url\":\"https://example.com\",\"custom_id\":\"ignored\",\"style\":1}]}\n```")
	if content != "" || embed == nil || embed.HasEmbedContent() || !reflect.DeepEqual(embed.Buttons, []GarminLinkButton{{Label: "Open", URL: "https://example.com"}}) {
		t.Fatalf("buttons-only reply = %q, %#v", content, embed)
	}
	if plain := GarminReplyPlainText(FormatGarminReply("", embed)); plain != "Open: https://example.com" {
		t.Fatalf("buttons-only plain text = %q", plain)
	}
	for range 6 {
		embed.Buttons = append(embed.Buttons, GarminLinkButton{Label: "Next", URL: "https://example.com/next"})
	}
	_, got := ParseGarminReply(FormatGarminReply("", embed))
	if got == nil || len(got.Buttons) != 5 || got.Buttons[0].Label != "Open" {
		t.Fatalf("button limit = %#v", got)
	}
}
