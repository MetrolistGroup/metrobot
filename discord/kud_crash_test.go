package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"
)

func TestCrashPointDistributionAndBounds(t *testing.T) {
	const space = uint64(1) << 52
	for _, test := range []struct {
		draw uint64
		want int64
	}{
		{0, 97 * int64(space)}, {space - 1, 100}, {space/2 - 1, 194},
	} {
		if got := crashPoint(test.draw); got != test.want {
			t.Fatalf("draw %d: got %d, want %d", test.draw, got, test.want)
		}
	}
	for _, target := range []int64{101, 200, 1000, crashCap} {
		// This boundary gives a 0.97 / multiplier chance to survive the target.
		boundary := uint64(97) * space / uint64(target)
		if crashPoint(boundary-1) < target || crashPoint(boundary) >= target {
			t.Fatalf("wrong survival boundary at %d", target)
		}
	}
}

func TestCrashSharedRoundCashoutAndLoss(t *testing.T) {
	b, client, _ := kudTestBot(t)
	if !b.handleSigmaCommand(client, kudTestMessage(80), ">>coins-crash 100") {
		t.Fatal("coins-crash alias not handled")
	}
	g := b.kudGames["crash:80"]
	if g == nil {
		t.Fatal("Crash did not open")
	}
	g.timer.Stop()
	g.crash.point = 500
	start := g.expires
	if err := b.playCrashAction(g, "cash", "1", "host", start.Add(-time.Second)); err == nil {
		t.Fatal("cash-out before start succeeded")
	}
	if err := b.playCrashAction(g, "join", "2", "player", start.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := b.playCrashAction(g, "join", "2", "player", start.Add(-time.Second)); err == nil {
		t.Fatal("duplicate entry succeeded")
	}
	if err := b.playCrashAction(g, "join", "3", "late", start); err == nil {
		t.Fatal("late entry succeeded")
	}
	if err := b.playCrashAction(g, "cash", "3", "outsider", start.Add(time.Second)); err == nil {
		t.Fatal("outsider cashed out")
	}
	if err := b.playCrashAction(g, "cash", "1", "host", start.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	paid := g.bet * g.crash.players["1"].cashout / 100
	if err := b.playCrashAction(g, "cash", "1", "host", start.Add(6*time.Second)); err == nil {
		t.Fatal("duplicate cash-out succeeded")
	}
	a, _ := b.DB.KudAccount("1")
	other, _ := b.DB.KudAccount("2")
	if a.Balance != 9900+paid || a.Played != 1 || a.Held != 0 || other.Held != 100 || other.Played != 0 || g.done {
		t.Fatalf("cash-out changed other entrants: host=%+v other=%+v done=%v", a, other, g.done)
	}
	if err := b.playCrashAction(g, "cash", "2", "late", g.crashEnd()); err == nil {
		t.Fatal("cash-out at the crash deadline succeeded")
	}
	other, _ = b.DB.KudAccount("2")
	if !g.done || other.Balance != 9900 || other.Played != 1 || other.Paid != 0 || other.Held != 0 {
		t.Fatalf("loss not settled: %+v done=%v", other, g.done)
	}
}

func TestCrashCashoutMatchesVisibleStep(t *testing.T) {
	b, client, _ := kudTestBot(t)
	b.handleSigmaCommand(client, kudTestMessage(87), ">>crash 1000")
	g := b.kudGames["crash:87"]
	g.timer.Stop()
	g.crash.point = 170
	g.advanceCrash(g.expires.Add(4 * time.Second))
	if g.crash.multiplier != 161 {
		t.Fatalf("expected the visible 1.61x step, got %d", g.crash.multiplier)
	}
	if err := b.playCrashAction(g, "cash", "1", "host", g.expires.Add(4600*time.Millisecond)); err != nil {
		t.Fatalf("visible step rejected cash-out: %v", err)
	}
	g.advanceCrash(g.expires.Add(6 * time.Second))
	if err := b.settleCrash(g); err != nil {
		t.Fatal(err)
	}
	a, err := b.DB.KudAccount("1")
	if err != nil {
		t.Fatal(err)
	}
	if !g.done || a.Balance != 10_610 || a.Paid != 1610 || a.Played != 1 || a.Held != 0 {
		t.Fatalf("cash-out was overwritten by the next crash step: %+v", a)
	}
}

func TestCrashInstantLossCapAndRestartRefund(t *testing.T) {
	for _, point := range []int64{100, crashCap, crashCap + 1} {
		t.Run(fmt.Sprint(point), func(t *testing.T) {
			b, client, _ := kudTestBot(t)
			b.handleSigmaCommand(client, kudTestMessage(81), ">>crash 100")
			g := b.kudGames["crash:81"]
			g.timer.Stop()
			g.crash.point = point
			g.advanceCrash(g.crashEnd())
			if err := b.settleCrash(g); err != nil {
				t.Fatal(err)
			}
			a, _ := b.DB.KudAccount("1")
			want := int64(9900)
			if point > crashCap {
				want += 10_000
			}
			if !g.done || a.Balance != want || a.Held != 0 || a.Played != 1 {
				t.Fatalf("point=%d account=%+v done=%v", point, a, g.done)
			}
		})
	}
	b, client, _ := kudTestBot(t)
	b.handleSigmaCommand(client, kudTestMessage(82), ">>crash all")
	g := b.kudGames["crash:82"]
	g.timer.Stop()
	if err := b.DB.RecoverKud(); err != nil {
		t.Fatal(err)
	}
	a, _ := b.DB.KudAccount("1")
	if a.Balance != 10_000 || a.Held != 0 || a.Played != 0 {
		t.Fatalf("restart did not refund: %+v", a)
	}
}

func TestCrashFailedSettlementRetainsCashoutAndRetries(t *testing.T) {
	database, control := kudFailureDB(t)
	g := &kudGame{id: "crash:1", kind: "crash", owner: "7", bet: 100, expires: time.Now(), crash: newKudCrash("7"), entrants: []kudEntrant{{userID: "7"}}}
	g.timer = time.AfterFunc(time.Hour, func() {})
	defer g.timer.Stop()
	g.crash.point = 500
	b := &Bot{DB: database, Logger: zap.NewNop(), kudGames: map[string]*kudGame{g.id: g}}
	if err := database.HoldKud(g.escrowID("7"), "crash", "7", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Exec(`CREATE TRIGGER crash_fail BEFORE INSERT ON kud_stats BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := b.playCrashAction(g, "cash", "7", "host", g.expires.Add(5*time.Second)); err == nil {
		t.Fatal("expected settlement failure")
	}
	locked := g.crash.players["7"].cashout
	a, _ := database.KudAccount("7")
	if locked <= 100 || a.Held != 100 || a.Balance != 900 {
		t.Fatalf("failure lost held stake or outcome: locked=%d account=%+v", locked, a)
	}
	if err := b.playCrashAction(g, "cash", "7", "host", g.expires.Add(6*time.Second)); err == nil || g.crash.players["7"].cashout != locked {
		t.Fatal("failed payout could be changed")
	}
	if _, err := control.Exec(`DROP TRIGGER crash_fail`); err != nil {
		t.Fatal(err)
	}
	g.advanceCrash(g.crashEnd())
	if err := b.settleCrash(g); err != nil {
		t.Fatal(err)
	}
	if err := b.settleCrash(g); err != nil {
		t.Fatal(err)
	}
	a, _ = database.KudAccount("7")
	if !g.done || a.Paid != locked || a.Balance != 900+locked || a.Played != 1 || a.Held != 0 {
		t.Fatalf("retry did not pay exactly once: %+v", a)
	}
}

func TestCrashTimerSettlesWithoutPlayerActions(t *testing.T) {
	for _, point := range []int64{100, 500, crashCap + 1} {
		t.Run(fmt.Sprint(point), func(t *testing.T) {
			b, client, _ := kudTestBot(t)
			b.handleSigmaCommand(client, kudTestMessage(84), ">>crash 100")
			g := b.kudGames["crash:84"]
			g.timer.Stop()
			g.crash.point = point
			if err := b.playCrashAction(g, "join", "2", "player", g.expires.Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			g.expires = time.Now().Add(-time.Minute)
			b.expireKudGame(client, g)
			if !g.done || b.kudGames[g.id] != nil {
				t.Fatal("timer did not close the round")
			}
			for _, user := range []string{"1", "2"} {
				a, err := b.DB.KudAccount(user)
				if err != nil {
					t.Fatal(err)
				}
				want := int64(9900)
				if point > crashCap {
					want += 10_000
				}
				if a.Balance != want || a.Held != 0 || a.Played != 1 {
					t.Fatalf("timer settlement for %s: %+v", user, a)
				}
			}
		})
	}
}

func TestCrashQueuedCashoutSurvivesAcknowledgementDelay(t *testing.T) {
	b, client, _ := kudTestBot(t)
	b.handleSigmaCommand(client, kudTestMessage(85), ">>crash 100")
	g := b.kudGames["crash:85"]
	g.timer.Stop()
	g.crash.point = 500
	g.expires = time.Now().Add(-time.Minute)
	var interaction discord.ComponentInteraction
	if err := json.Unmarshal([]byte(`{"id":"900","application_id":"4","token":"test","type":3,"user":{"id":"1","username":"host"},"channel":{"id":"5","type":0},"message":{"id":"100","flags":32768},"data":{"custom_id":"kud:crash:85:0:cash","component_type":2}}`), &interaction); err != nil {
		t.Fatal(err)
	}
	event := &events.ComponentInteractionCreate{ComponentInteraction: interaction}
	input := b.queueCrashInput(event)
	if input == nil {
		t.Fatal("cash-out was not queued before acknowledgement")
	}
	// Simulate an arrival five seconds into a round whose acknowledgement is still pending after the crash.
	input.at = g.expires.Add(5 * time.Second)
	acknowledging, release := make(chan struct{}), make(chan struct{})
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/callback") {
			close(acknowledging)
			<-release
			return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"100","channel_id":"5"}`)), Request: r}, nil
	})})
	finished := make(chan struct{})
	go func() {
		b.handleCrashComponent(client, event, input)
		close(finished)
	}()
	<-acknowledging
	b.expireKudGame(client, g)
	close(release)
	<-finished
	a, err := b.DB.KudAccount("1")
	if err != nil {
		t.Fatal(err)
	}
	if input.err != nil || !g.done || a.Played != 1 || a.Paid != 161 || a.Balance != 10_061 || a.Held != 0 {
		t.Fatalf("delayed acknowledgement changed timely cash-out: err=%v account=%+v", input.err, a)
	}
}

func TestCrashFailedAcknowledgementPublishesClosedRound(t *testing.T) {
	b, client, _ := kudTestBot(t)
	b.handleSigmaCommand(client, kudTestMessage(86), ">>crash 100")
	g := b.kudGames["crash:86"]
	g.timer.Stop()
	g.crash.point = 100
	g.expires = time.Now().Add(-time.Minute)
	var interaction discord.ComponentInteraction
	if err := json.Unmarshal([]byte(`{"id":"900","application_id":"4","token":"test","type":3,"user":{"id":"1","username":"host"},"channel":{"id":"5","type":0},"message":{"id":"100","flags":32768},"data":{"custom_id":"kud:crash:86:0:cash","component_type":2}}`), &interaction); err != nil {
		t.Fatal(err)
	}
	var updated bool
	client.Rest = testREST(&http.Client{Transport: garminRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusForbidden, `{"code":10062,"message":"Unknown interaction"}`
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/channels/5/messages/100") {
			updated = true
			status, body = http.StatusOK, `{"id":"100","channel_id":"5"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	b.handleKudComponent(client, &events.ComponentInteractionCreate{ComponentInteraction: interaction})
	a, err := b.DB.KudAccount("1")
	if err != nil {
		t.Fatal(err)
	}
	if !updated || !g.done || b.kudGames[g.id] != nil || a.Played != 1 || a.Held != 0 || a.Balance != 9900 {
		t.Fatalf("failed acknowledgement left round open: updated=%v done=%v account=%+v", updated, g.done, a)
	}
}

func TestCrashLoanDeductionAndRenderBounds(t *testing.T) {
	b, client, _ := kudTestBot(t)
	if err := b.DB.FundKudLoan("2", "1", 500); err != nil {
		t.Fatal(err)
	}
	b.handleSigmaCommand(client, kudTestMessage(83), ">>crash 100")
	g := b.kudGames["crash:83"]
	g.timer.Stop()
	g.crash.point = 500
	if err := b.playCrashAction(g, "cash", "1", "host", g.expires.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	a, _ := b.DB.KudAccount("1")
	payout := g.crash.players["1"].cashout
	if a.Debt != 500-payout/5 || a.Balance != 10_400+payout-payout/5 || a.Paid != payout {
		t.Fatalf("loan repayment or stats incorrect: %+v", a)
	}
	g.bet = db.MaxKudBet
	g.entrants = nil
	for i := range 32 {
		id := fmt.Sprintf("%020d", i)
		g.entrants = append(g.entrants, kudEntrant{userID: id})
		g.crash.players[id] = &crashPlayer{cashout: crashCap, decided: true, paid: true, balance: 9_000_000_000_000_000}
	}
	g.crash.ended = true
	components := g.render()
	data, err := json.Marshal(components)
	if err != nil {
		t.Fatal(err)
	}
	body := components[0].(discord.ContainerComponent).Components[0].(discord.TextDisplayComponent).Content
	if len(utf16.Encode([]rune(body))) > 4000 || !strings.Contains(string(data), "Coins Crash") || strings.Contains(string(data), `"parse":["users"]`) {
		t.Fatalf("Crash results exceed message bounds or allow mentions: %d bytes", len(data))
	}
}
