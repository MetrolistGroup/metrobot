package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

func TestSourceDeleteDuringBoardPostRemovesPost(t *testing.T) {
	for _, board := range []reactionBoard{{"starboard", "⭐"}, {"sobboard", "😭"}} {
		t.Run(board.name, func(t *testing.T) {
			d, err := db.Open(filepath.Join(t.TempDir(), "board.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Close() })
			c := mustTestClient(t)
			postStarted, releasePost := make(chan struct{}), make(chan struct{})
			var posts, deletes atomic.Int64
			source := discord.Message{ID: 100, ChannelID: 200, Content: "removed source", Author: discord.User{ID: 400, Username: "user"},
				Reactions: []discord.MessageReaction{{Count: 3, Emoji: discord.Emoji{Name: board.emoji}}}}
			c.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{"id":"800","channel_id":"500"}`
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v10/channels/200/messages/100":
					raw, _ := json.Marshal(source)
					body = string(raw)
				case "POST /api/v10/channels/500/messages":
					posts.Add(1)
					close(postStarted)
					<-releasePost
				case "DELETE /api/v10/channels/500/messages/800":
					deletes.Add(1)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			b := &Bot{DB: d, Config: testConfig(config.Config{DiscordGuildID: "300", StarboardChannelID: "500"}), Logger: zap.NewNop()}

			created := make(chan struct{})
			go func() { b.handleOriginalMessageReaction(c, "200", "100", "300", board); close(created) }()
			<-postStarted
			deleted := make(chan struct{})
			go func() {
				b.handleMessageDelete(c, &events.MessageDelete{GenericMessage: &events.GenericMessage{MessageID: 100, ChannelID: 200}})
				close(deleted)
			}()
			select {
			case <-deleted:
				t.Fatal("source deletion did not wait for the in-flight board post")
			case <-time.After(50 * time.Millisecond):
			}
			close(releasePost)
			<-created
			<-deleted

			entry, err := d.GetBoardEntry(board.name, "100")
			if err != nil || entry != nil || posts.Load() != 1 || deletes.Load() != 1 {
				t.Fatalf("entry=%+v posts=%d deletes=%d err=%v", entry, posts.Load(), deletes.Load(), err)
			}
		})
	}
}

func TestFailedBoardPostCleanupKeepsEntryForRefresh(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	postID := "800"
	if _, err := d.AddBoardEntry("starboard", "100", "200", "300", "400", "x", 3, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateBoardEntry("starboard", "100", 3, &postID); err != nil {
		t.Fatal(err)
	}
	var deleteStatus atomic.Int64
	deleteStatus.Store(http.StatusInternalServerError)
	c := mustTestClient(t)
	c.Caches.AddChannel(testChannel(t, map[string]any{"id": "200", "guild_id": "300", "type": discord.ChannelTypeGuildText}))
	c.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{}`
		switch r.Method + " " + r.URL.Path {
		case "DELETE /api/v10/channels/500/messages/800":
			status = int(deleteStatus.Load())
		case "GET /api/v10/channels/200/messages/100":
			status, body = http.StatusNotFound, `{"code":10008,"message":"Unknown Message"}`
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	b := &Bot{DB: d, Config: testConfig(config.Config{DiscordGuildID: "300", StarboardChannelID: "500"}), Logger: zap.NewNop()}

	b.handleMessageDelete(c, &events.MessageDelete{GenericMessage: &events.GenericMessage{MessageID: 100, ChannelID: 200}})
	if entry, _ := d.GetBoardEntry("starboard", "100"); entry == nil {
		t.Fatal("entry dropped although the board post is still up")
	}

	deleteStatus.Store(http.StatusNoContent)
	if err := b.RefreshAllStarboard(c); err != nil {
		t.Fatal(err)
	}
	if entry, _ := d.GetBoardEntry("starboard", "100"); entry != nil {
		t.Fatal("refresh did not clean up the entry for a deleted source")
	}
}

func TestBoardReactionRemovalRequiresConfirmedCounts(t *testing.T) {
	for _, failure := range []string{"source unavailable", "board unavailable", "source deleted"} {
		t.Run(failure, func(t *testing.T) {
			d, err := db.Open(filepath.Join(t.TempDir(), "board.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Close() })
			postID := "800"
			if _, err := d.AddBoardEntry("starboard", "100", "200", "300", "400", "x", 3, 1); err != nil {
				t.Fatal(err)
			}
			if err := d.UpdateBoardEntry("starboard", "100", 3, &postID); err != nil {
				t.Fatal(err)
			}
			deleted := false
			c := mustTestClient(t)
			c.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v10/channels/200/messages/100":
					if failure == "source unavailable" {
						return nil, io.ErrUnexpectedEOF
					}
					if failure == "source deleted" {
						return reminderResponse(r, http.StatusNotFound, `{"code":10008,"message":"Unknown Message"}`)
					}
					return reminderResponse(r, http.StatusOK, `{"id":"100","channel_id":"200","reactions":[{"count":2,"emoji":{"name":"⭐"}}]}`)
				case "GET /api/v10/channels/500/messages/800":
					return nil, io.ErrUnexpectedEOF
				case "DELETE /api/v10/channels/500/messages/800":
					deleted = true
					return reminderResponse(r, http.StatusNoContent, "")
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL)
					return nil, nil
				}
			})})
			b := &Bot{DB: d, Config: testConfig(config.Config{DiscordGuildID: "300", StarboardChannelID: "500"}), Logger: zap.NewNop()}
			b.handleOriginalMessageReactionRemove(c, "200", "100", reactionBoard{"starboard", "⭐"})
			entry, err := d.GetBoardEntry("starboard", "100")
			if failure == "source deleted" {
				if err != nil || entry != nil || !deleted {
					t.Fatalf("deleted source kept its board post: entry=%+v deleted=%v err=%v", entry, deleted, err)
				}
			} else if err != nil || entry == nil || entry.StarCount != 3 || deleted {
				t.Fatalf("failed lookup changed board: entry=%+v deleted=%v err=%v", entry, deleted, err)
			}
		})
	}
}
