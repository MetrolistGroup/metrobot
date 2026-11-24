package discord

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

type interactionRecorder struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]string
}

func (r *interactionRecorder) record(req *http.Request) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := req.Method + " " + req.URL.Path
	r.requests = append(r.requests, key)
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		if r.bodies == nil {
			r.bodies = map[string]string{}
		}
		r.bodies[key] = string(raw)
	}
	return key
}

func TestPurgeReportsOnlyConfirmedDeletes(t *testing.T) {
	recent := func(offset time.Duration) string { return snowflake.New(time.Now().Add(-offset)).String() }
	ids := []string{recent(time.Minute), recent(2 * time.Minute), recent(20 * 24 * time.Hour)}
	for _, tt := range []struct {
		name        string
		bulkStatus  int
		deleteFails map[string]bool
		want        string
	}{
		{name: "bulk denied", bulkStatus: http.StatusForbidden, deleteFails: map[string]bool{ids[0]: true, ids[1]: true, ids[2]: true}, want: "Deleted 0 messages. Could not delete 3."},
		{name: "old message fails", bulkStatus: http.StatusNoContent, deleteFails: map[string]bool{ids[2]: true}, want: "Deleted 2 messages. Could not delete 1."},
		{name: "all deleted", bulkStatus: http.StatusNoContent, want: "Deleted 3 messages."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, err := db.Open(filepath.Join(t.TempDir(), "purge.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Close() })
			authority := testGuildAuthority{guildID: "300", ownerID: "99", roles: []testRole{{id: "60", permissions: discord.PermissionManageMessages | discord.PermissionViewChannel, position: 2}}, members: map[string][]string{"400": {"60"}}}
			rec := &interactionRecorder{}
			var bulkIDs []string
			c := mustTestClient(t)
			c.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if response, ok := authority.response(r); ok {
					return response, nil
				}
				key := rec.record(r)
				status, body := http.StatusOK, `{}`
				switch {
				case key == "POST /api/v10/interactions/17/test/callback":
					status, body = http.StatusNoContent, ""
				case key == "GET /api/v10/channels/200":
					body = `{"id":"200","guild_id":"300","type":0,"permission_overwrites":[]}`
				case key == "GET /api/v10/channels/200/messages":
					body = fmt.Sprintf(`[{"id":%q,"channel_id":"200"},{"id":%q,"channel_id":"200"},{"id":%q,"channel_id":"200"}]`, ids[0], ids[1], ids[2])
				case key == "POST /api/v10/channels/200/messages/bulk-delete":
					var v struct{ Messages []string }
					_ = json.Unmarshal([]byte(rec.bodies[key]), &v)
					bulkIDs = v.Messages
					status, body = tt.bulkStatus, `{"code":50013,"message":"Missing Permissions"}`
				case strings.HasPrefix(key, "DELETE /api/v10/channels/200/messages/"):
					status, body = http.StatusNoContent, ""
					if tt.deleteFails[strings.TrimPrefix(key, "DELETE /api/v10/channels/200/messages/")] {
						status, body = http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`
					}
				case key == "PATCH /api/v10/webhooks/1/test/messages/@original":
				default:
					t.Errorf("unexpected request %s", key)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{DB: d, Config: testConfig(config.Config{DiscordGuildID: "300"}), Logger: zap.NewNop()}
			i := testCommandInteraction(t, c, `{"id":"17","application_id":"1","token":"test","type":2,"guild_id":"300","channel":{"id":"200","type":0},"member":{"user":{"id":"400"},"roles":[],"permissions":"0"},"data":{"name":"purge","type":1,"options":[{"name":"count","type":4,"value":3}]}}`)
			b.onInteractionCreate(c, i)

			var update struct{ Content string }
			_ = json.Unmarshal([]byte(rec.bodies["PATCH /api/v10/webhooks/1/test/messages/@original"]), &update)
			if update.Content != tt.want {
				t.Fatalf("purge reply = %q, want %q", update.Content, tt.want)
			}
			if len(bulkIDs) != 2 {
				t.Fatalf("bulk delete should only include the two recent messages, got %v", bulkIDs)
			}
		})
	}
}

func TestDeferredCommandsAckBeforeWorkAndKeepErrorsPrivate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		stay    bool
		workErr error
		want    []string
	}{
		{name: "public success", stay: true, want: []string{"POST /api/v10/interactions/17/test/callback", "WORK", "PATCH /api/v10/webhooks/1/test/messages/@original"}},
		{name: "ephemeral error", workErr: errors.New("github down"), want: []string{"POST /api/v10/interactions/17/test/callback", "WORK", "PATCH /api/v10/webhooks/1/test/messages/@original"}},
		{name: "public error", stay: true, workErr: errors.New("github down"), want: []string{"POST /api/v10/interactions/17/test/callback", "WORK", "PATCH /api/v10/webhooks/1/test/messages/@original", "DELETE /api/v10/webhooks/1/test/messages/@original", "POST /api/v10/webhooks/1/test"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := &interactionRecorder{}
			completed := false
			c := mustTestClient(t)
			c.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				rec.record(r)
				if r.Method == http.MethodPatch {
					completed = true
				}
				if r.Method == http.MethodPost && r.URL.Path == "/api/v10/webhooks/1/test" && !completed {
					t.Error("error follow-up would inherit the public deferred response's visibility")
				}
				status, body := http.StatusOK, `{"id":"900","channel_id":"200"}`
				if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/callback") || r.Method == http.MethodDelete {
					status, body = http.StatusNoContent, ""
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{Logger: zap.NewNop()}
			i := testCommandInteraction(t, c, `{"id":"17","application_id":"1","token":"test","type":2,"guild_id":"300","channel":{"id":"200","type":0},"member":{"user":{"id":"400"},"roles":[],"permissions":"0"},"data":{"name":"latest","type":1}}`)
			b.respondDeferredText(c, i, !tt.stay, "latest error", "Error fetching latest version.", func() (string, error) {
				rec.mu.Lock()
				rec.requests = append(rec.requests, "WORK")
				rec.mu.Unlock()
				return "v14", tt.workErr
			})
			if strings.Join(rec.requests, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("requests = %v, want %v", rec.requests, tt.want)
			}
			var deferred struct {
				Type int
				Data struct{ Flags int }
			}
			_ = json.Unmarshal([]byte(rec.bodies["POST /api/v10/interactions/17/test/callback"]), &deferred)
			ephemeral := deferred.Data.Flags&64 != 0
			if deferred.Type != 5 || ephemeral == tt.stay {
				t.Fatalf("deferred type=%d ephemeral=%v, stay=%v", deferred.Type, ephemeral, tt.stay)
			}
			if tt.workErr != nil && tt.stay {
				var followup struct {
					Content string
					Flags   int
				}
				_ = json.Unmarshal([]byte(rec.bodies["POST /api/v10/webhooks/1/test"]), &followup)
				if followup.Flags&64 == 0 || followup.Content != "Error fetching latest version." {
					t.Fatalf("error follow-up = %+v, want ephemeral error", followup)
				}
			}
		})
	}
}

func TestDiscordChatModPatternRequiresCommandBoundary(t *testing.T) {
	for _, content := range []string{"!warnings", "!bananas", "!kicked", "!timeouts", "!muted"} {
		if m := chatModPattern.FindStringSubmatch(content); m != nil {
			t.Errorf("%q matched as %q", content, m[1])
		}
	}
	for content, want := range map[string]string{"!warn": "", "!timeout 1h spam": "1h spam", "!kick\tspam": "spam"} {
		if m := chatModPattern.FindStringSubmatch(content); m == nil || m[2] != want {
			t.Errorf("%q = %q, want args %q", content, m, want)
		}
	}
}
