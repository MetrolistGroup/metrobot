package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/internal/music"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestMusicControlSchemaBoundsAndHelp(t *testing.T) {
	queue := musicCommands()[2].(discord.SlashCommandCreate)
	seen := map[string]bool{}
	for _, option := range queue.Options {
		switch opt := option.(type) {
		case discord.ApplicationCommandOptionString:
			if opt.Name == "action" {
				for _, choice := range opt.Choices {
					seen[choice.Value] = true
				}
			}
		case discord.ApplicationCommandOptionInt:
			maxValue := 100
			if opt.Name == "seconds" {
				maxValue = int(music.MaxDuration / time.Second)
			} else if opt.Name != "percent" {
				continue
			}
			if opt.MinValue == nil || *opt.MinValue != 0 || opt.MaxValue == nil || *opt.MaxValue != maxValue {
				t.Fatalf("unbounded playback option: %+v", opt)
			}
			seen[opt.Name] = true
		}
	}
	for _, name := range []string{"seek", "volume", "seconds", "percent"} {
		if !seen[name] {
			t.Fatalf("missing %s schema", name)
		}
	}
	for _, page := range helpPages {
		if page.title == "Music" && strings.Contains(page.body, "seek seconds:N") && strings.Contains(page.body, "volume percent:N") {
			return
		}
	}
	t.Fatal("help omitted seek/volume")
}

func TestMusicControlsDispatchTheirOwnOptions(t *testing.T) {
	client := mustTestClient(t)
	client.Caches.AddGuild(discord.Guild{ID: 300})
	client.Caches.AddChannel(testChannel(t, map[string]any{"id": "100", "guild_id": "300", "type": 2}))
	client.Caches.AddVoiceState(discord.VoiceState{GuildID: 300, UserID: 42, ChannelID: ptr(snowflake.ID(100))})
	started := make(chan music.Playback, 5)
	p := music.NewPlayer(context.Background(), func(context.Context, uint64) (music.Sink, error) { return musicTestSink{}, nil }, func(ctx context.Context, track music.Track, playback music.Playback, info func(music.Track), wait func(context.Context) error, sink music.Sink) error {
		track.ID, track.Duration = "abcdefghijk", 10*time.Second
		info(track)
		if err := sink.WriteFrame(ctx, []byte{0xfc}); err != nil {
			return err
		}
		started <- playback
		<-ctx.Done()
		return ctx.Err()
	})
	t.Cleanup(p.Close)
	_ = p.Enqueue(100, "track")
	<-started
	var response string
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Content != "" {
			response = payload.Content
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})})
	b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop(), music: p}
	for _, test := range []struct {
		action, options, message string
		playback                 *music.Playback
	}{
		{"seek", "", "Specify seconds", nil},
		{"volume", "", "Specify percent", nil},
		{"seek", `,{"name":"seconds","type":4,"value":2},{"name":"position","type":4,"value":7},{"name":"percent","type":4,"value":9}`, "Queue action applied: seek", &music.Playback{Offset: 2 * time.Second, Volume: 100}},
		{"volume", `,{"name":"percent","type":4,"value":40},{"name":"seconds","type":4,"value":3},{"name":"position","type":4,"value":4}`, "Queue action applied: volume", &music.Playback{Offset: 2*time.Second + 20*time.Millisecond, Volume: 40}},
	} {
		interaction := fmt.Sprintf(`{"id":"1","application_id":"2","token":"fixture","type":2,"guild_id":"300","member":{"user":{"id":"42","username":"fixture"}},"data":{"id":"3","name":"queue","type":1,"options":[{"name":"action","type":3,"value":%q}%s]}}`, test.action, test.options)
		b.onInteractionCreate(client, testCommandInteraction(t, client, interaction))
		if !strings.Contains(response, test.message) {
			t.Fatalf("%s: %q", test.action, response)
		}
		if test.playback != nil {
			select {
			case got := <-started:
				if got != *test.playback {
					t.Fatalf("%s used another action's option: %+v", test.action, got)
				}
			case <-time.After(time.Second):
				t.Fatal("control did not restart")
			}
		}
	}
}
