package discord

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestYouTubeRepostSurvivesUncertainSourceDeletion(t *testing.T) {
	for _, tt := range []struct {
		name         string
		deleteStatus int // 0 means the response was lost
		getStatus    int
		wantSuccess  bool
	}{
		{name: "lost response, source gone", getStatus: http.StatusNotFound, wantSuccess: true},
		{name: "lost response, source kept", getStatus: http.StatusOK, wantSuccess: false},
		{name: "lost response, unknown state", getStatus: http.StatusBadGateway, wantSuccess: true},
		{name: "source already deleted", deleteStatus: http.StatusNotFound, wantSuccess: true},
		{name: "deletion denied", deleteStatus: http.StatusForbidden, wantSuccess: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := mustTestClient(t)
			client.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: 900}})
			client.Caches.AddChannel(testChannel(t, map[string]any{"id": "200", "guild_id": "300", "type": discord.ChannelTypeGuildText}))
			repostDeleted := false
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, `{}`
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v10/channels/200/webhooks":
					body = `[{"id":"700","type":1,"channel_id":"200","name":"` + youtubeCleanerWebhookName + `","token":"fixture","user":{"id":"900"}}]`
				case "POST /api/v10/webhooks/700/fixture":
					body = `{"id":"800","channel_id":"200"}`
				case "DELETE /api/v10/channels/200/messages/100":
					if tt.deleteStatus == 0 {
						return nil, io.ErrUnexpectedEOF
					}
					status, body = tt.deleteStatus, `{"code":0,"message":"fixture"}`
				case "GET /api/v10/channels/200/messages/100":
					status, body = tt.getStatus, `{"id":"100","channel_id":"200"}`
					if tt.getStatus != http.StatusOK {
						body = `{"code":10008,"message":"Unknown Message"}`
					}
				case "DELETE /api/v10/webhooks/700/fixture/messages/800":
					repostDeleted = true
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{Config: testConfig(config.Config{DiscordGuildID: "300"}), Logger: zap.NewNop()}
			message := &discord.Message{ID: 100, ChannelID: 200, GuildID: ptr(snowflake.ID(300)), Content: "https://youtu.be/video?si=source", Author: discord.User{ID: 400, Username: "author"}}
			if got := b.repostCleanYouTubeLinks(client, message); got != tt.wantSuccess {
				t.Fatalf("success = %v, want %v", got, tt.wantSuccess)
			}
			if repostDeleted == tt.wantSuccess {
				t.Fatalf("repost deleted = %v with success %v", repostDeleted, tt.wantSuccess)
			}
		})
	}
}
