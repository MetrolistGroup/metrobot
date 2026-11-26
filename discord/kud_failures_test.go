package discord

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/bot"
	dgo "github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

func kudFailureDB(t *testing.T) (*db.DB, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "review.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	control, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { control.Close() })
	for _, user := range []string{"7", "8", "9"} {
		if _, err := database.AddKudos(user, 1000); err != nil {
			t.Fatal(err)
		}
	}
	return database, control
}

func TestKudFailedBoardUpdateRefreshesVisibleButtons(t *testing.T) {
	database, _ := kudFailureDB(t)
	g := &kudGame{id: "mines:1", kind: "mines", owner: "7", channelID: 5, messageID: 38, bet: 100, mines: 3, multiplier: 1, expires: time.Now().Add(time.Hour)}
	g.timer = time.AfterFunc(time.Hour, func() {})
	defer g.timer.Stop()
	if err := database.HoldKud(g.id, g.kind, g.owner, g.bet); err != nil {
		t.Fatal(err)
	}
	b := &Bot{DB: database, Logger: zap.NewNop(), kudGames: map[string]*kudGame{g.id: g}}
	var patches int
	var followup string
	client := mustTestClient(t)
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusNoContent, ""
		if r.Method == http.MethodPatch {
			patches++
			status, body = http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`
		} else if strings.Contains(r.URL.Path, "/webhooks/") {
			data, _ := io.ReadAll(r.Body)
			followup = string(data)
			status, body = http.StatusOK, `{"id":"44","channel_id":"5"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	makeInteraction := func(action string) *events.ComponentInteractionCreate {
		payload := `{"id":"17","application_id":"1","token":"token","type":3,"user":{"id":"7","username":"host"},"channel":{"id":"5","type":0},"message":{"id":"38","flags":32768},"data":{"custom_id":"kud:mines:1:0:` + action + `","component_type":2}}`
		var interaction dgo.ComponentInteraction
		if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
			t.Fatal(err)
		}
		return &events.ComponentInteractionCreate{ComponentInteraction: interaction}
	}
	b.handleKudComponent(client, makeInteraction("tile0"))
	b.handleKudComponent(client, makeInteraction("tile1"))
	if g.version != 1 || g.picks != 1 || patches != 2 || !strings.Contains(followup, "board changed") {
		t.Fatalf("unexpected state version=%d picks=%d patches=%d followup=%s", g.version, g.picks, patches, followup)
	}

}

func TestKudFailedFightSettlementFreezesAdmission(t *testing.T) {
	database, control := kudFailureDB(t)
	g := &kudGame{id: "fight:1", kind: "fight", owner: "7", bet: 100, entrants: []kudEntrant{{userID: "7", name: "host"}}}
	g.timer = time.AfterFunc(time.Hour, func() {})
	defer g.timer.Stop()
	b := &Bot{DB: database, Logger: zap.NewNop(), kudGames: map[string]*kudGame{g.id: g}}
	if err := database.HoldKud(g.id, g.kind, g.owner, g.bet); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Exec(`CREATE TRIGGER review_fail BEFORE INSERT ON kud_stats BEGIN SELECT RAISE(ABORT,'injected settlement failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := b.playKudAction(g, "join", "8", "player2"); err == nil {
		t.Fatal("expected injected failure")
	}
	if _, err := control.Exec(`DROP TRIGGER review_fail`); err != nil {
		t.Fatal(err)
	}
	retryErr := b.playKudAction(g, "join", "8", "player2")
	thirdErr := b.playKudAction(g, "join", "9", "player3")
	account, err := database.KudAccount("9")
	if err != nil {
		t.Fatal(err)
	}
	if retryErr == nil || !strings.Contains(retryErr.Error(), "waiting for settlement") || thirdErr == nil || !strings.Contains(thirdErr.Error(), "waiting for settlement") || g.done || len(g.entrants) != 2 || account.Held != 0 {
		t.Fatalf("retry=%v third=%v done=%v entrants=%d held=%d", retryErr, thirdErr, g.done, len(g.entrants), account.Held)
	}
	g.expires = time.Now().Add(-time.Second)
	b.expireKudGame(kudFailureClient(t), g)
	if !g.done {
		t.Fatal("original fight outcome did not settle after recovery")
	}
	for _, user := range []string{"7", "8"} {
		a, _ := database.KudAccount(user)
		if a.Held != 0 || a.Played != 1 {
			t.Fatalf("fight recovery: %+v", a)
		}
	}
}

func TestKudFailedMineLossCannotCashOut(t *testing.T) {
	database, control := kudFailureDB(t)
	g := &kudGame{id: "mines:1", kind: "mines", owner: "7", bet: 100, mines: 3, picks: 1, multiplier: 1}
	g.revealed[0] = true
	g.board[1], g.board[2], g.board[3] = true, true, true
	g.timer = time.AfterFunc(time.Hour, func() {})
	defer g.timer.Stop()
	b := &Bot{DB: database, Logger: zap.NewNop(), kudGames: map[string]*kudGame{g.id: g}}
	if err := database.HoldKud(g.id, g.kind, g.owner, g.bet); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Exec(`CREATE TRIGGER review_fail BEFORE INSERT ON kud_stats BEGIN SELECT RAISE(ABORT,'injected settlement failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := b.playKudAction(g, "tile1", "7", "host"); err == nil {
		t.Fatal("expected injected failure")
	}
	if _, err := control.Exec(`DROP TRIGGER review_fail`); err != nil {
		t.Fatal(err)
	}
	if err := b.playKudAction(g, "cash", "7", "host"); err == nil {
		t.Fatal("losing round could cash out")
	}
	g.expires = time.Now().Add(-time.Second)
	b.expireKudGame(kudFailureClient(t), g)
	account, err := database.KudAccount("7")
	if err != nil {
		t.Fatal(err)
	}
	if !g.done || account.Balance != 900 {
		t.Fatalf("done=%v balance=%d", g.done, account.Balance)
	}

}

func kudFailureClient(t *testing.T) *bot.Client {
	client := mustTestClient(t)
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"38","channel_id":"5"}`)), Request: r}, nil
	})})
	return client
}
