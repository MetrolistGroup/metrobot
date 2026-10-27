package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestOCRTranslateMessageAction(t *testing.T) {
	for _, mode := range []string{"success", "long result", "provider error", "empty result", "truncated", "busy", "cooldown"} {
		t.Run(mode, func(t *testing.T) {
			client := mustTestClient(t)
			deferred, delivered, called := false, false, false
			result := "Original\nHola @everyone\n\nEnglish\nHello @everyone"
			if mode == "long result" {
				result = strings.Repeat("transcribed text ", 200)
			}
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch r.Method {
				case http.MethodPost:
					var response struct {
						Type discord.InteractionResponseType `json:"type"`
						Data discord.MessageCreate           `json:"data"`
					}
					if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
						t.Error(err)
					}
					deferred = response.Type == discord.InteractionResponseTypeDeferredCreateMessage && response.Data.Flags.Has(discord.MessageFlagEphemeral)
				case http.MethodPatch:
					var update discord.MessageUpdate
					if mode == "long result" {
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
						}
						defer r.MultipartForm.RemoveAll()
						if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &update); err != nil {
							t.Error(err)
						}
						file, _, err := r.FormFile("files[0]")
						if err != nil {
							t.Error(err)
						} else {
							defer file.Close()
							data, _ := io.ReadAll(file)
							if string(data) != strings.TrimSpace(result) {
								t.Error("long translation was truncated")
							}
						}
					} else if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Error(err)
					}
					if (mode == "success" || mode == "long result") && (update.AllowedMentions == nil || len(update.AllowedMentions.Parse) != 0) {
						t.Error("OCR output can ping users")
					}
					delivered = update.Content != nil && *update.Content != ""
					if mode == "success" && value(update.Content) != result {
						t.Errorf("translation = %q", value(update.Content))
					}
					if mode == "truncated" && !strings.HasPrefix(value(update.Content), "Partial result:") {
						t.Error("model truncation was not disclosed")
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			})})
			b := &Bot{
				Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop(),
				garminAILastUsed: make(map[string]time.Time), garminAISlots: make(chan struct{}, 1),
				garminAI: garminAITestFunc(func(ctx context.Context, request cmd.GarminAIRequest) (*cmd.GarminAICompletion, error) {
					called = true
					if _, ok := ctx.Deadline(); !ok || request.SystemPrompt != ocrTranslatePrompt || len(request.Tools) != 0 || request.Context != "" || len(request.Messages) != 1 || !request.DisableReasoning {
						t.Errorf("OCR request leaked context/tools or lacks timeout: %#v", request)
					}
					if !reflect.DeepEqual(request.Messages[0].Images, []string{"https://cdn.discordapp.com/attachments/1/2/image.png"}) || request.Messages[0].Content != "selected message only" {
						t.Errorf("OCR request used wrong input: %#v", request.Messages)
					}
					if mode == "provider error" {
						return nil, errors.New("provider unavailable")
					}
					answer := result
					if mode == "empty result" {
						answer = ""
					}
					finishReason := "stop"
					if mode == "truncated" {
						finishReason = "length"
					}
					return &cmd.GarminAICompletion{Message: cmd.GarminAIMessage{Content: answer}, FinishReason: finishReason}, nil
				}),
			}
			if mode == "busy" {
				b.garminAISlots <- struct{}{}
			}
			if mode == "cooldown" {
				b.garminAILastUsed["500"] = time.Now()
			}
			interaction := testCommandInteraction(t, client, `{
				"id":"100","application_id":"123456789012345678","token":"token","type":2,
				"guild_id":"300","channel":{"id":"200","type":0},"member":{"user":{"id":"500"}},
				"data":{"name":"OCR Translate","type":3,"target_id":"50","resolved":{"messages":{"50":{
					"id":"50","content":"selected message only","author":{"id":"400"},
					"attachments":[{"id":"2","filename":"image.png","url":"https://cdn.discordapp.com/attachments/1/2/image.png"}]
				}}}}
			}`)
			b.onInteractionCreate(client, interaction)
			wantCall := mode != "busy" && mode != "cooldown"
			if !deferred || !delivered || called != wantCall {
				t.Fatalf("deferred=%v delivered=%v AI called=%v want=%v", deferred, delivered, called, wantCall)
			}
			if len(b.garminAIContexts) != 0 || len(b.garminAIUserContexts) != 0 {
				t.Fatal("private OCR result entered chat history")
			}
		})
	}
}

func TestOCRImageSelection(t *testing.T) {
	message := &discord.Message{
		Attachments: []discord.Attachment{
			{Filename: "image.png", URL: "https://cdn.discordapp.com/a.png"},
			{Filename: "secret.txt", URL: "https://cdn.discordapp.com/a.txt"},
			{Filename: "image.jpg", URL: "http://localhost/a.jpg"},
		},
		Embeds: []discord.Embed{
			{Image: &discord.EmbedResource{URL: "https://example.com/image.png"}},
			{Image: &discord.EmbedResource{URL: "https://cdn.discordapp.com/a.png"}},
			{Thumbnail: &discord.EmbedResource{URL: "https://example.com/unrelated.png"}},
			{Image: &discord.EmbedResource{URL: "file:///etc/passwd"}},
		},
	}
	want := []string{"https://cdn.discordapp.com/a.png", "https://example.com/image.png"}
	if got := ocrImageURLs(message); !reflect.DeepEqual(got, want) {
		t.Fatalf("OCR images = %v, want %v", got, want)
	}
}
