package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/discord"
)

func TestGarminDiscordContextIncludesDatesAndConversationGaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	previous := now.Add(-6 * time.Hour)
	message := &discord.Message{CreatedAt: now, Author: discord.User{ID: snowflakeID("56"), Username: "speaker"}, ReferencedMessage: &discord.Message{ID: snowflakeID("150"), CreatedAt: previous, Author: discord.User{ID: snowflakeID("57"), Username: "other"}}}
	tracked := []cmd.GarminAIMessage{garminAIUserMessage(&discord.Message{CreatedAt: previous, Author: message.Author}, "earlier plan"), garminAIUserMessage(message, "what did we decide?")}
	_, data, _ := strings.Cut(garminDiscordContext(nil, nil, message, tracked), "\n")
	var context struct {
		Now        string            `json:"current_utc_time"`
		Current    string            `json:"current_message_timestamp"`
		Timestamps map[string]string `json:"tracked_message_timestamps"`
		Reference  struct {
			Timestamp string `json:"timestamp"`
		} `json:"replied_to_message"`
	}
	if err := json.Unmarshal([]byte(data), &context); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, context.Now); err != nil {
		t.Fatalf("current time lacks full date: %q", context.Now)
	}
	if context.Current != now.Format(time.RFC3339) || context.Timestamps["0"] != previous.Format(time.RFC3339) || context.Timestamps["1"] != context.Current || context.Reference.Timestamp != context.Timestamps["0"] {
		t.Fatalf("conversation timestamps = %#v", context)
	}
}

func TestGetGarminDiscordProfileUsesOnlyDiscordData(t *testing.T) {
	const targetID = "123456789012345678"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/members/"):
			_, _ = w.Write([]byte(`{"user":{"id":"123456789012345678","username":"target","global_name":"Global Name"},"nick":"Server Name","roles":["100000000000000036"]}`))
		case strings.HasSuffix(r.URL.Path, "/roles"):
			_, _ = w.Write([]byte(`[{"id":"100000000000000036","name":"she/her"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	session, err := newTestClient(t)

	if err != nil {
		t.Fatal(err)
	}
	session.Rest = testREST(server.Client())
	session.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: session.Rest.HTTPClient().Transport, target: server.URL}
	bot := &Bot{Config: &config.Config{DiscordGuildID: "100000000000000012"}}

	profile, err := bot.getGarminDiscordProfile(session, targetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"target", "Server Name", "she/her", "server roles", `"bio":null`} {
		if !strings.Contains(profile, expected) {
			t.Errorf("Discord profile missing %q: %s", expected, profile)
		}
	}
	for _, removed := range []string{"Global Name", "global_name", "remembered_info", "user-saved", "personalization"} {
		if strings.Contains(profile, removed) {
			t.Errorf("Discord profile contains removed field %q: %s", removed, profile)
		}
	}
}
