package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/auditsearch"
	"github.com/MetrolistGroup/metrobot/config"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

func auditTestREST(server *httptest.Server) rest.Rest {
	return rest.New(rest.NewClient("test-bot-token", rest.WithURL(server.URL), rest.WithHTTPClient(server.Client()), rest.WithRateLimiterConfigOpts(rest.WithMaxRetries(0))))
}

func TestAuditHistorySourceMappingAndAuthenticatedREST(t *testing.T) {
	channelID := ""
	before := snowflake.New(time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot test-bot-token" || r.Method != http.MethodGet {
			t.Error("did not reuse authenticated read-only REST")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/channels/" + channelID:
			fmt.Fprintf(w, `{"id":%q,"guild_id":"111","type":0,"name":"logs"}`, channelID)
		case "/channels/" + channelID + "/messages":
			if r.URL.Query().Get("before") != before.String() || r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("around") != "" || r.URL.Query().Get("after") != "" {
				t.Errorf("wrong history bounds: %s", r.URL)
			}
			fmt.Fprint(w, `[{"id":"222","channel_id":"`+channelID+`","timestamp":"2026-03-29T00:00:00Z","content":"historic log"}]`)
		default:
			t.Errorf("unexpected path %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	b := &Bot{Config: &config.Config{DiscordGuildID: "111", DiscordLogChannel: "333"}, Client: &bot.Client{Rest: auditTestREST(server)}}
	history := b.AuditHistory()
	for source, want := range map[string]string{"message": messageLogChannelID, "reaction": reactionLogChannelID, "server": serverLogChannelID, "moderation": "333"} {
		channelID = want
		channel, err := history.Channel(context.Background(), source)
		if err != nil || channel.ID.String() != want || channel.GuildID.String() != "111" {
			t.Fatalf("source=%s channel=%+v err=%v", source, channel, err)
		}
		messages, err := history.Messages(context.Background(), channel.ID, before, 100)
		if err != nil || len(messages) != 1 || messages[0].Content != "historic log" {
			t.Fatalf("messages=%+v err=%v", messages, err)
		}
	}
}

func TestAuditHistoryUnavailableHTTPFailuresAndGuildIsolation(t *testing.T) {
	calls, status, guild := 0, http.StatusOK, "111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			fmt.Fprint(w, `{"message":"private-bot-token: secret detail","code":50001}`)
			return
		}
		fmt.Fprintf(w, `{"id":%q,"guild_id":%q,"type":0}`, messageLogChannelID, guild)
	}))
	defer server.Close()
	h := &dashboardAuditHistory{cfg: &config.Config{DiscordGuildID: "111"}, rest: auditTestREST(server)}
	for _, source := range []string{"moderation", "https://evil.example", "999"} {
		if _, err := h.Channel(context.Background(), source); !errors.Is(err, auditsearch.ErrUnavailable) {
			t.Fatalf("err=%v", err)
		}
	}
	if calls != 0 {
		t.Fatal("unavailable or unknown source made a network request")
	}
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		status = code
		if _, err := h.Channel(context.Background(), "message"); !errors.Is(err, auditsearch.ErrFetch) || strings.Contains(err.Error(), "private-bot-token") {
			t.Fatalf("status=%d err=%v", status, err)
		}
		if _, err := h.Messages(context.Background(), 1, 2, 100); !errors.Is(err, auditsearch.ErrFetch) {
			t.Fatalf("status=%d err=%v", status, err)
		}
	}
	status, guild = http.StatusOK, "another-guild"
	if _, err := h.Channel(context.Background(), "message"); err == nil {
		t.Fatal("accepted invalid guild")
	}
	guild = "222"
	if _, err := h.Channel(context.Background(), "message"); !errors.Is(err, auditsearch.ErrUnavailable) {
		t.Fatalf("failed guild isolation: %v", err)
	}
	if _, err := (&Bot{}).AuditHistory().Channel(context.Background(), "server"); !errors.Is(err, auditsearch.ErrUnavailable) {
		t.Fatalf("disabled bot err=%v", err)
	}
}

func TestAuditHistoryRESTRespectsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	h := &dashboardAuditHistory{cfg: &config.Config{DiscordGuildID: "111"}, rest: auditTestREST(server)}
	for _, channel := range []bool{true, false} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		started := time.Now()
		var err error
		if channel {
			_, err = h.Channel(ctx, "server")
		} else {
			_, err = h.Messages(ctx, 1, 2, 100)
		}
		cancel()
		if err == nil || time.Since(started) > time.Second {
			t.Fatalf("request ignored context: %v", err)
		}
	}
}
