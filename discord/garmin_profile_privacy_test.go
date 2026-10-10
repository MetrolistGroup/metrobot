package discord

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MetrolistGroup/metrobot/config"
)

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
