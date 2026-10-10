package discord

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestNativeThreadWebhookFailuresKeepOriginalAndRollBackRepost(t *testing.T) {
	for _, failAt := range []string{"execute", "delete"} {
		t.Run(failAt, func(t *testing.T) {
			client := mustTestClient(t)
			client.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: 900}})
			client.Caches.AddChannel(testChannel(t, map[string]any{"id": "200", "guild_id": "300", "parent_id": "201", "type": discord.ChannelTypeGuildPublicThread}))
			var paths []string
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				paths = append(paths, r.Method+" "+r.URL.Path)
				status, body := http.StatusOK, `{}`
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/201/webhooks"):
					body = `[{"id":"700","type":1,"channel_id":"201","name":"` + youtubeCleanerWebhookName + `","token":"fixture","user":{"id":"900"}}]`
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/webhooks/700/fixture"):
					if r.URL.Query().Get("thread_id") != "200" || r.URL.Query().Get("wait") != "true" {
						t.Error("repost did not wait for confirmation in the original thread")
					}
					body = `{"id":"800","channel_id":"200"}`
					if failAt == "execute" {
						status, body = http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`
					}
				case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/channels/200/messages/100"):
					status, body = http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`
				case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/webhooks/700/fixture/messages/800"):
					if r.URL.Query().Get("thread_id") != "200" {
						t.Error("rollback omitted the thread ID")
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop()}
			message := &discord.Message{ID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Content: "https://youtu.be/video?si=source", Author: discord.User{ID: 400, Username: "author"}}
			if b.repostCleanYouTubeLinks(client, message) {
				t.Fatal("failed repost/delete reported success")
			}
			want := []string{"GET /api/v10/channels/201/webhooks", "POST /api/v10/webhooks/700/fixture"}
			if failAt == "delete" {
				want = append(want, "DELETE /api/v10/channels/200/messages/100", "DELETE /api/v10/webhooks/700/fixture/messages/800")
			}
			if !slices.Equal(paths, want) {
				t.Fatalf("webhook sequencing changed: %v", paths)
			}
		})
	}
}
