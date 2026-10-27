package discord

import (
	"bytes"
	"encoding/json"
	"image"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"
)

func TestNukeReplyRoutingAndReferenceFetch(t *testing.T) {
	for _, fetchReference := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded target", true: "fetched target"}[fetchReference], func(t *testing.T) {
			client := mustTestClient(t)
			var avatar bytes.Buffer
			if err := png.Encode(&avatar, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
				t.Fatal(err)
			}
			sent := make(chan discord.MessageCreate, 1)
			client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{}`
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/200/messages/50"):
					body = `{"id":"50","channel_id":"200","author":{"id":"400","username":"target"}}`
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/members/"):
					return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
				case r.Method == http.MethodGet && r.URL.Host == "cdn.discordapp.com":
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(avatar.Bytes())), Request: r}, nil
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/200/messages"):
					if err := r.ParseMultipartForm(5 << 20); err != nil {
						t.Error(err)
					}
					defer r.MultipartForm.RemoveAll()
					var message discord.MessageCreate
					if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &message); err != nil {
						t.Error(err)
					}
					file, header, err := r.FormFile("files[0]")
					if err != nil {
						t.Error(err)
					} else {
						defer file.Close()
						if _, err := gif.DecodeAll(file); err != nil || header.Filename != "nuke.gif" {
							t.Errorf("GIF upload: filename=%s error=%v", header.Filename, err)
						}
					}
					sent <- message
					body = `{"id":"70","channel_id":"200"}`
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop()}
			command := &discord.Message{
				ID: 60, ChannelID: 200, GuildID: snowflakePtr("300"), Author: discord.User{ID: 500}, Content: "garmin nuke them",
				MessageReference: &discord.MessageReference{MessageID: snowflakePtr("50")},
			}
			if !fetchReference {
				command.ReferencedMessage = &discord.Message{ID: 50, Author: discord.User{ID: 400, Username: "target"}}
			}
			b.onMessageCreate(client, command)
			select {
			case message := <-sent:
				if message.MessageReference == nil || value(message.MessageReference.MessageID) != 50 || message.AllowedMentions == nil || message.AllowedMentions.RepliedUser || len(message.AllowedMentions.Parse) != 0 {
					t.Fatalf("target or mention policy lost: %#v", message)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("nuke callout did not send GIF")
			}
		})
	}
}

func TestNukeSlashCommandAndAvatarFailure(t *testing.T) {
	client := mustTestClient(t)
	deferred, uploaded := false, false
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		status := 200
		switch r.Method {
		case http.MethodGet:
			status = 404
		case http.MethodPost:
			var response struct {
				Type discord.InteractionResponseType `json:"type"`
			}
			if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
				t.Error(err)
			}
			deferred = response.Type == discord.InteractionResponseTypeDeferredCreateMessage
		case http.MethodPatch:
			uploaded = strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;")
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop()}
	interaction := testCommandInteraction(t, client, `{
		"id":"100","application_id":"123456789012345678","token":"token","type":2,
		"guild_id":"300","channel":{"id":"200","type":0},"member":{"user":{"id":"500"}},
		"data":{"name":"nuke","type":1,"options":[{"name":"user","type":6,"value":"400"}],
		"resolved":{"users":{"400":{"id":"400","username":"target"}}}}
	}`)
	b.onInteractionCreate(client, interaction)
	if !deferred || !uploaded || b.nukeBusy.Load() {
		t.Fatalf("slash nuke deferred=%v uploaded=%v busy=%v", deferred, uploaded, b.nukeBusy.Load())
	}
}

func TestNukeMissingReplyAndBusy(t *testing.T) {
	client := mustTestClient(t)
	var replies []string
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var message discord.MessageCreate
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Error(err)
		}
		replies = append(replies, message.Content)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"70"}`)), Request: r}, nil
	})})
	b := &Bot{Logger: zap.NewNop()}
	command := &discord.Message{ID: 60, ChannelID: 200}
	b.handleNukeReply(client, command)
	b.nukeBusy.Store(true)
	b.handleNukeReply(client, command)
	if len(replies) != 2 || !strings.Contains(replies[0], "Reply to") || !strings.Contains(replies[1], "Already rendering") {
		t.Fatalf("unexpected replies: %v", replies)
	}
}
