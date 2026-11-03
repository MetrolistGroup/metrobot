package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestHandleGarminAIEmbedReply(t *testing.T) {
	richEmbed := &cmd.GarminEmbed{
		Title: "status", Description: "damn, that works.", URL: "https://metrolist.cc", Color: 123,
		Fields:    []cmd.GarminEmbedField{{Name: "release", Value: "available", Inline: true}},
		Author:    &cmd.GarminEmbedAuthor{Name: "Metrolist", URL: "http://metrolist.cc/team", IconURL: "https://metrolist.cc/author.png"},
		Footer:    &cmd.GarminEmbedFooter{Text: "footer", IconURL: "https://metrolist.cc/footer.png"},
		Thumbnail: &cmd.GarminEmbedResource{URL: "https://metrolist.cc/thumb.png"},
		Image:     &cmd.GarminEmbedResource{URL: "https://metrolist.cc/image.png"}, Timestamp: "2026-11-03T12:00:00Z",
		Buttons: []cmd.GarminLinkButton{{Label: "Open", URL: "https://metrolist.cc"}, {Label: "News", URL: "http://metrolist.cc/news"}},
	}
	buttonsOnly := &cmd.GarminEmbed{}
	for range 5 {
		buttonsOnly.Buttons = append(buttonsOnly.Buttons, cmd.GarminLinkButton{Label: "Open", URL: "https://metrolist.cc"})
	}
	for _, test := range []struct {
		name, content string
		embed         *cmd.GarminEmbed
	}{
		{"embed only", "", richEmbed},
		{"clipped text and embed", strings.Repeat("a short answer. ", 200), richEmbed},
		{"buttons only", "", buttonsOnly},
		{"text and buttons", "here you go.", buttonsOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, embed := test.content, test.embed
			session := mustTestClient(t)
			cacheTestGuild(session, discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: discord.Guild{ID: 12}, Roles: []discord.Role{{ID: 13, Name: "member"}}}})
			session.Caches.AddChannel(testChannel(t, map[string]any{"id": "200", "guild_id": "12", "name": "bots"}))
			var delivered discord.MessageCreate
			sent := 0
			session.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := "{}"
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages"):
					body = "[]"
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
					if err := json.NewDecoder(r.Body).Decode(&delivered); err != nil {
						t.Error(err)
					}
					sent++
					body = `{"id":"150","channel_id":"200"}`
				case strings.HasSuffix(r.URL.Path, "/typing"):
				default:
					t.Errorf("unexpected REST request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			answer := cmd.FormatGarminReply(content, embed)
			b := &Bot{Logger: zap.NewNop(), garminAISlots: make(chan struct{}, 1), garminAILastUsed: make(map[string]time.Time), garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if !strings.Contains(request.SystemPrompt, cmd.GarminEmbedInstructions) {
					t.Error("embed format missing from prompt")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: answer}}, nil
			})}
			message := &discord.Message{ID: 100, GuildID: snowflakePtr("12"), ChannelID: 200, Content: "garmin, show an embed", Author: discord.User{ID: 56, Username: "speaker"}, Member: &discord.Member{}}
			b.handleGarminAI(session, message, []cmd.GarminAIMessage{garminAIUserMessage(message, "show an embed")})
			if sent != 1 {
				t.Fatalf("delivered %d replies: %#v", sent, delivered)
			}
			if embed.HasEmbedContent() {
				if len(delivered.Embeds) != 1 {
					t.Fatalf("delivered embeds = %#v", delivered.Embeds)
				}
				e := delivered.Embeds[0]
				if e.Title != embed.Title || e.Description != embed.Description || e.URL != embed.URL || e.Color != 123 || len(e.Fields) != 1 || e.Fields[0].Name != "release" || e.Fields[0].Value != "available" || e.Fields[0].Inline == nil || !*e.Fields[0].Inline {
					t.Fatalf("embed fields = %#v", e)
				}
				if e.Author == nil || e.Author.Name != embed.Author.Name || e.Author.URL != embed.Author.URL || e.Author.IconURL != embed.Author.IconURL || e.Footer == nil || e.Footer.Text != embed.Footer.Text || e.Footer.IconURL != embed.Footer.IconURL || e.Thumbnail == nil || e.Thumbnail.URL != embed.Thumbnail.URL || e.Image == nil || e.Image.URL != embed.Image.URL || e.Timestamp == nil || e.Timestamp.Format(time.RFC3339) != embed.Timestamp {
					t.Fatalf("richer embed fields = %#v", e)
				}
			} else if len(delivered.Embeds) != 0 {
				t.Fatalf("buttons-only reply sent an embed: %#v", delivered.Embeds)
			}
			if len(delivered.Components) != 1 {
				t.Fatalf("action rows = %#v", delivered.Components)
			}
			row, ok := delivered.Components[0].(discord.ActionRowComponent)
			if !ok || len(row.Components) != len(embed.Buttons) {
				t.Fatalf("link buttons = %#v", delivered.Components)
			}
			for i, component := range row.Components {
				button, ok := component.(discord.ButtonComponent)
				if !ok || button.Style != discord.ButtonStyleLink || button.Label != embed.Buttons[i].Label || button.URL != embed.Buttons[i].URL || button.CustomID != "" {
					t.Fatalf("link button = %#v", component)
				}
			}
			if len(delivered.Content) > garminAIMaxContent || !utf8.ValidString(delivered.Content) || strings.Contains(delivered.Content, "```embed") || len(content) > garminAIMaxContent && !strings.HasSuffix(delivered.Content, "...") {
				t.Fatalf("bad reply text: %q", delivered.Content)
			}
			if delivered.AllowedMentions == nil || len(delivered.AllowedMentions.Parse) != 0 || delivered.MessageReference == nil {
				t.Fatal("reply lost mention suppression or reference")
			}
			history, ok := b.garminAIHistory(message, "150", false)
			want := strings.TrimSpace(delivered.Content + "\n" + embed.PlainText())
			if !ok || history[len(history)-1].Content != want {
				t.Fatalf("history did not retain delivered text: %#v, want %q", history, want)
			}
		})
	}
}

func TestGarminClipUsesWordBoundary(t *testing.T) {
	if got := truncateGarminAIResponseTo("hello world, how are you today?", 25); got != "hello world, how are..." {
		t.Fatalf("clip = %q", got)
	}
	for _, limit := range []int{-1, 0, 1, 3, 4} {
		if got := truncateGarminAIResponseTo("😀 long answer", limit); len(got) > max(0, limit) || !utf8.ValidString(got) {
			t.Fatalf("clip at %d = %q", limit, got)
		}
	}
}

func TestHandleGarminAILongGeneratedSupportReplyIsClipped(t *testing.T) {
	b, client, delivery := newSupportDecisionTestBot(t)
	b.garminAI = garminAITestFunc(func(context.Context, cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
		return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: strings.Repeat("a supported answer. ", 200)}}, nil
	})
	m := supportTestMessage(100)
	b.handleGarminAI(client, m, []cmd.GarminAIMessage{garminAIUserMessage(m, m.Content)})
	delivery.mu.Lock()
	bodies := append([]string(nil), delivery.bodies...)
	delivery.mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("sent %d support replies", len(bodies))
	}
	var payload discord.MessageCreate
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Content) > garminAIMaxContent || !strings.HasSuffix(payload.Content, "...") || len(payload.Files) != 0 {
		t.Fatalf("long generated support reply = %#v", payload)
	}
	history, ok := b.garminAIHistory(m, "1001", false)
	if !ok || history[len(history)-1].Content != payload.Content {
		t.Fatalf("support history differs from delivered text: %#v", history)
	}
}
