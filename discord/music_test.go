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

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/internal/music"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

func TestMusicQueueOutputIsBoundedAndPaginated(t *testing.T) {
	label := strings.Repeat("𝄞", 100) + " @everyone **[link]**"
	s := music.Snapshot{Channel: ^uint64(0), Current: &music.Track{Title: label, Duration: 3 * time.Hour}, Elapsed: 3 * time.Hour, Volume: 100, Paused: true, Loop: "queue", Status: "disconnecting", LastError: label}
	for i := 0; i < music.MaxQueue; i++ {
		s.Pending = append(s.Pending, music.Track{Title: label})
	}
	for _, page := range []int{-10, 1, 20, 999} {
		out := formatMusicQueue(s, page)
		if len(out) > 1950 || !utf8.ValidString(out) || strings.Contains(out, "@everyone") {
			t.Fatalf("unsafe queue output (%d bytes): %q", len(out), out)
		}
	}
	if out := formatMusicQueue(s, 2); !strings.Contains(out, "page 2/20") || !strings.Contains(out, "\n6. ") || strings.Contains(out, "\n1. ") {
		t.Fatal("pagination lost absolute positions")
	}
	if out := formatMusicQueue(s, 1); !strings.Contains(out, "volume: 100%") || !strings.Contains(out, "3h0m0s / 3h0m0s") || !strings.Contains(out, "seek seconds:N") || !strings.Contains(out, "volume percent:N") {
		t.Fatal("queue omitted playback controls/status")
	}
}

func TestMusicChannelRequiresNormalVoiceAndJoinPermissions(t *testing.T) {
	client := mustTestClient(t)
	client.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: 900}})
	b := &Bot{Config: &config.Config{DiscordGuildID: "300"}}
	guild := snowflake.ID(300)
	client.Caches.AddGuild(discord.Guild{ID: guild})
	client.Caches.AddRole(discord.Role{ID: guild, GuildID: guild, Permissions: discord.PermissionConnect | discord.PermissionSpeak})
	client.Caches.AddMember(discord.Member{GuildID: guild, User: discord.User{ID: client.ID()}})
	client.Caches.AddChannel(testChannel(t, map[string]any{"id": "100", "guild_id": "300", "type": 2}))
	client.Caches.AddChannel(testChannel(t, map[string]any{"id": "101", "guild_id": "300", "type": 13}))
	if _, err := b.musicChannel(client, 42, true); err == nil {
		t.Fatal("non-voice caller accepted")
	}
	client.Caches.AddVoiceState(discord.VoiceState{GuildID: guild, UserID: 42, ChannelID: ptr(snowflake.ID(101))})
	if _, err := b.musicChannel(client, 42, true); err == nil {
		t.Fatal("stage channel accepted")
	}
	client.Caches.AddVoiceState(discord.VoiceState{GuildID: guild, UserID: 42, ChannelID: ptr(snowflake.ID(100))})
	if id, err := b.musicChannel(client, 42, true); err != nil || id != 100 {
		t.Fatalf("normal channel rejected: %v %v", id, err)
	}
	client.Caches.AddRole(discord.Role{ID: guild, GuildID: guild, Permissions: discord.PermissionConnect})
	if _, err := b.musicChannel(client, 42, true); err == nil {
		t.Fatal("missing Speak accepted")
	}
	if _, err := b.musicChannel(client, 42, false); err != nil {
		t.Fatal("lost bot permissions prevented stop/control")
	}
}

type musicTestSink struct{}

func (musicTestSink) WriteFrame(context.Context, []byte) error { return nil }
func (musicTestSink) Silence(context.Context) error            { return nil }
func (musicTestSink) Close()                                   {}

func TestMusicVoiceKickMoveAndEmptyCleanup(t *testing.T) {
	for _, reason := range []string{"kick", "move", "empty", "mute"} {
		t.Run(reason, func(t *testing.T) {
			client := mustTestClient(t)
			client.Caches.SetSelfUser(discord.OAuth2User{User: discord.User{ID: 900}})
			started := make(chan struct{})
			p := music.NewPlayer(context.Background(), func(context.Context, uint64) (music.Sink, error) { return musicTestSink{}, nil }, func(ctx context.Context, track music.Track, playback music.Playback, info func(music.Track), wait func(context.Context) error, sink music.Sink) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
			t.Cleanup(p.Close)
			b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, music: p}
			_ = p.Enqueue(100, "test")
			<-started
			state := discord.VoiceState{GuildID: 300, UserID: client.ID(), ChannelID: ptr(snowflake.ID(100))}
			old := state
			client.Caches.AddVoiceState(state)
			stale := state
			stale.ChannelID = nil
			b.onMusicVoiceState(client, &events.GuildVoiceStateUpdate{GenericGuildVoiceState: &events.GenericGuildVoiceState{VoiceState: stale}, OldVoiceState: old})
			if s := p.Snapshot(); s.Channel == 0 || s.Status == "disconnecting" {
				t.Fatal("stale leave canceled a newer cached join")
			}
			switch reason {
			case "kick":
				state.ChannelID = nil
			case "move":
				state.ChannelID = ptr(snowflake.ID(200))
			case "mute":
				state.GuildMute = true
			case "empty":
				state.UserID = 42
				state.ChannelID = nil
				old.UserID = 42
				client.Caches.AddVoiceState(discord.VoiceState{GuildID: 300, UserID: 43, ChannelID: ptr(snowflake.ID(100))})
				// Uncached listeners should not be treated as bots.
				b.onMusicVoiceState(client, &events.GuildVoiceStateUpdate{GenericGuildVoiceState: &events.GenericGuildVoiceState{VoiceState: state}, OldVoiceState: old})
				if s := p.Snapshot(); s.Channel == 0 || s.Status == "disconnecting" {
					t.Fatal("disconnected while listener remained")
				}
				client.Caches.RemoveVoiceState(300, 43)
			}
			if state.ChannelID == nil {
				client.Caches.RemoveVoiceState(300, state.UserID)
			} else {
				client.Caches.AddVoiceState(state)
			}
			b.onMusicVoiceState(client, &events.GuildVoiceStateUpdate{GenericGuildVoiceState: &events.GenericGuildVoiceState{VoiceState: state}, OldVoiceState: old})
			deadline := time.Now().Add(time.Second)
			for p.Snapshot().Channel != 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if p.Snapshot().Channel != 0 {
				t.Fatal("voice lifecycle did not cancel/reap")
			}
		})
	}
}

func TestMusicDispatchDefersAndReportsUnavailableWithoutNetwork(t *testing.T) {
	client := mustTestClient(t)
	calls := 0
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if calls == 1 {
			if string(payload["type"]) != "5" {
				t.Fatal("music interaction not immediately deferred")
			}
		} else if calls == 2 {
			if !strings.Contains(string(payload["content"]), "unavailable") {
				t.Fatal("non-music build lied about playback")
			}
		} else {
			t.Fatal("unexpected request")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})})
	b := &Bot{Config: &config.Config{DiscordGuildID: "300"}, Logger: zap.NewNop()}
	i := testCommandInteraction(t, client, `{"id":"1","application_id":"2","token":"fixture","type":2,"guild_id":"300","member":{"user":{"id":"42","username":"fixture"}},"data":{"id":"3","name":"play","type":1}}`)
	b.onInteractionCreate(client, i)
	if calls != 2 {
		t.Fatalf("dispatch made %d requests", calls)
	}
	b.Config.DiscordGuildID = "400"
	b.onInteractionCreate(client, i)
	if calls != 2 {
		t.Fatal("music escaped configured guild")
	}
}
