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
	for _, content := range []string{"", strings.Repeat("a short answer. ", 200)} {
		t.Run(map[bool]string{true: "embed only", false: "clipped text and embed"}[content == ""], func(t *testing.T) {
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
			embed := &cmd.GarminEmbed{Title: "status", Description: "damn, that works.", URL: "https://metrolist.cc", Color: 123, Fields: []cmd.GarminEmbedField{{Name: "release", Value: "available", Inline: true}}}
			answer := cmd.FormatGarminReply(content, embed)
			b := &Bot{Logger: zap.NewNop(), garminAISlots: make(chan struct{}, 1), garminAILastUsed: make(map[string]time.Time), garminAI: garminAITestFunc(func(_ context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
				if !strings.Contains(request.SystemPrompt, cmd.GarminEmbedInstructions) {
					t.Error("embed format missing from prompt")
				}
				return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Role: "assistant", Content: answer}}, nil
			})}
			message := &discord.Message{ID: 100, GuildID: snowflakePtr("12"), ChannelID: 200, Content: "garmin, show an embed", Author: discord.User{ID: 56, Username: "speaker"}, Member: &discord.Member{}}
			b.handleGarminAI(session, message, []cmd.GarminAIMessage{garminAIUserMessage(message, "show an embed")})
			if sent != 1 || len(delivered.Embeds) != 1 || delivered.Embeds[0].Title != "status" || delivered.Embeds[0].Description != embed.Description || delivered.Embeds[0].URL != embed.URL || delivered.Embeds[0].Color != 123 || len(delivered.Embeds[0].Fields) != 1 || delivered.Embeds[0].Fields[0].Inline == nil || !*delivered.Embeds[0].Fields[0].Inline {
				t.Fatalf("delivered %d replies: %#v", sent, delivered)
			}
			if len(delivered.Content) > garminAIMaxContent || !utf8.ValidString(delivered.Content) || strings.Contains(delivered.Content, "```embed") || content != "" && !strings.HasSuffix(delivered.Content, "...") {
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
