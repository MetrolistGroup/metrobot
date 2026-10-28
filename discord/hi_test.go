package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestHiExactMatch(t *testing.T) {
	for _, text := range []string{"Hi", "hi", "HI", "hI", " hi", "Hi ", "Hi!", "hi there", "\nhi"} {
		if got := isHiMessage(&discord.Message{Content: text}); got != (text == "Hi" || text == "hi") {
			t.Errorf("isHiMessage(%q)=%v", text, got)
		}
	}
	if isHiMessage(&discord.Message{Content: "Hi", Attachments: []discord.Attachment{{Filename: "image.png"}}}) ||
		isHiMessage(&discord.Message{Content: "hi", StickerItems: []discord.MessageSticker{{ID: 1}}}) {
		t.Fatal("media message treated as just Hi")
	}
}

func TestHiWebhookIdentityAndThreadRouting(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(map[bool]string{false: "reuse", true: "create in thread"}[create], func(t *testing.T) {
			client := mustTestClient(t)
			client.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: 900}})
			fields := map[string]any{"id": "200", "guild_id": "300", "type": discord.ChannelTypeGuildText}
			parent := "200"
			if create {
				fields["type"], fields["parent_id"] = discord.ChannelTypeGuildPublicThread, "201"
				parent = "201"
			}
			client.Caches.AddChannel(testChannel(t, fields))
			sent := make(chan struct{}, 2)
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{}`
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/"+parent+"/webhooks"):
					body = `[{"id":"701","type":1,"channel_id":"` + parent + `","name":"the Hi","token":"other-owner","user":{"id":"901"}}]`
					if !create {
						body = `[{"id":"700","type":1,"channel_id":"200","name":"the Hi","token":"fixture","user":{"id":"900"}}]`
					}
				case create && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/201/webhooks"):
					var request discord.WebhookCreate
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Name != hiWebhookName {
						t.Errorf("webhook create: %+v, %v", request, err)
					}
					body = `{"id":"700","type":1,"channel_id":"201","name":"the Hi","token":"fixture","user":{"id":"900"}}`
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/webhooks/700/fixture"):
					var message discord.WebhookMessageCreate
					if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
						t.Error(err)
					}
					if message.Content != "Hi" || message.Username != "the Hi" || message.AvatarURL != hiWebhookAvatar || message.AllowedMentions == nil || len(message.AllowedMentions.Parse) != 0 {
						t.Errorf("wrong greeting identity: %+v", message)
					}
					if create && r.URL.Query().Get("thread_id") != "200" || !create && r.URL.Query().Get("thread_id") != "" {
						t.Error("greeting sent to wrong channel/thread")
					}
					sent <- struct{}{}
					body = `{"id":"800","channel_id":"200"}`
				default:
					t.Errorf("unexpected request (original must not be deleted): %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop()}
			message := &discord.Message{ID: 100, ChannelID: 200, GuildID: snowflakePtr("300"), Author: discord.User{ID: 400}, Content: "Hi"}
			b.onMessageCreate(client, message)
			select {
			case <-sent:
			case <-time.After(3 * time.Second):
				t.Fatal("no Hi webhook reply")
			}
			message.Author.Bot = true
			b.onMessageCreate(client, message)
			message.Author.Bot = false
			message.WebhookID = snowflakePtr("700")
			b.onMessageCreate(client, message)
			if len(sent) != 0 {
				t.Fatal("Hi webhook looped on bot or webhook messages")
			}
		})
	}
}

func TestHiWebhookFailureDoesNotDeleteOrFallThrough(t *testing.T) {
	client := mustTestClient(t)
	client.Caches.AddChannel(testChannel(t, map[string]any{"id": "200", "guild_id": "300"}))
	calls := 0
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/webhooks") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":50013,"message":"Missing Permissions"}`)), Request: r}, nil
	})})
	b := &Bot{Logger: zap.NewNop()}
	b.sendHiWebhook(client, &discord.Message{ID: 100, ChannelID: 200})
	if calls != 1 {
		t.Fatalf("failed greeting made %d requests", calls)
	}
}
