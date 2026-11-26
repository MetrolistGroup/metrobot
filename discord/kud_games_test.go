package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/gif"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/config"
	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/internal/kudplinko"
	"github.com/MetrolistGroup/metrobot/util"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
)

type kudDelivery struct {
	mu       sync.Mutex
	payloads []map[string]any
	clip     []byte
}

func kudTestBot(t *testing.T) (*Bot, *bot.Client, *kudDelivery) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "kud.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, user := range []string{"1", "2", "3"} {
		if _, err := database.AddKudos(user, 10_000); err != nil {
			t.Fatal(err)
		}
	}
	deliveries := &kudDelivery{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		var clip []byte
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Error(err)
			}
			defer r.MultipartForm.RemoveAll()
			if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &payload); err != nil {
				t.Error(err)
			}
			for _, files := range r.MultipartForm.File {
				for _, file := range files {
					f, _ := file.Open()
					clip, _ = io.ReadAll(f)
					f.Close()
				}
			}
		} else if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		deliveries.mu.Lock()
		deliveries.payloads = append(deliveries.payloads, payload)
		if clip != nil {
			deliveries.clip = clip
		}
		deliveries.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/callback") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fmt.Fprint(w, `{"id":"100","channel_id":"5","flags":32768}`)
	}))
	t.Cleanup(server.Close)
	client := mustTestClient(t)
	client.Rest = testREST(server.Client())
	client.Rest.HTTPClient().Transport = rewriteDiscordTransport{base: client.Rest.HTTPClient().Transport, target: server.URL}
	b := &Bot{DB: database, Logger: zap.NewNop(), Config: testConfig(config.Config{DiscordGuildID: "12"})}
	t.Cleanup(func() {
		for _, game := range b.kudGames {
			game.timer.Stop()
		}
	})
	return b, client, deliveries
}

func kudTestMessage(id snowflake.ID) *discord.Message {
	return &discord.Message{ID: id, ChannelID: 5, GuildID: ptr(snowflake.ID(12)), Author: discord.User{ID: 1, Username: "host"}}
}

func kudTestClick(t *testing.T, b *Bot, client *bot.Client, g *kudGame, version int, action, user string) {
	t.Helper()
	payload := fmt.Sprintf(`{"id":"900","application_id":"4","token":"test","type":3,"user":{"id":%q,"username":"player"},"channel":{"id":"5","type":0},"message":{"id":"100","flags":32768},"data":{"custom_id":%q,"component_type":2}}`, user, fmt.Sprintf("kud:%s:%d:%s", g.id, version, action))
	var interaction discord.ComponentInteraction
	if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
		t.Fatal(err)
	}
	if !b.handleKudComponent(client, &events.ComponentInteractionCreate{ComponentInteraction: interaction}) {
		t.Fatal("Kud button not handled")
	}
}

func TestMinesFirstPickAndRiskPayout(t *testing.T) {
	for mines := 1; mines <= 10; mines++ {
		for index := range 16 {
			for range 20 {
				g := &kudGame{mines: mines, multiplier: 1, bet: 100}
				if !g.pickMine(index) || g.board[index] || g.payout() != 100 {
					t.Fatal("first pick was unsafe or gave risk-free profit")
				}
				count := 0
				for _, mine := range g.board {
					if mine {
						count++
					}
				}
				if count != mines {
					t.Fatal("wrong mine count")
				}
				for cell := range 16 {
					if cell != index && !g.board[cell] {
						if !g.pickMine(cell) || g.multiplier <= 1 {
							t.Fatal("risk did not increase payout")
						}
						break
					}
				}
			}
		}
	}
}

func TestKudMinesButtonsOwnershipReplayAndCashout(t *testing.T) {
	b, client, deliveries := kudTestBot(t)
	b.handleKudGame(client, kudTestMessage(10), "mines", "100 3")
	g := b.kudGames["mines:10"]
	if g == nil {
		t.Fatal("Mines did not open")
	}
	kudTestClick(t, b, client, g, 0, "tile0", "2")
	if g.picks != 0 {
		t.Fatal("other user played the round")
	}
	kudTestClick(t, b, client, g, 0, "tile0", "1")
	kudTestClick(t, b, client, g, 0, "tile1", "1")
	if g.picks != 1 || g.version != 1 {
		t.Fatal("stale button changed round")
	}
	kudTestClick(t, b, client, g, 1, "cash", "1")
	kudTestClick(t, b, client, g, 1, "cash", "1")
	a, _ := b.DB.KudAccount("1")
	if !g.done || a.Balance != 10_000 || a.Played != 1 || a.Draws != 1 || a.Held != 0 {
		t.Fatalf("cash out failed: %+v", a)
	}
	deliveries.mu.Lock()
	defer deliveries.mu.Unlock()
	initial := deliveries.payloads[0]
	if initial["flags"] != float64(discord.MessageFlagIsComponentsV2) || initial["content"] != nil || initial["embeds"] != nil {
		t.Fatal("game is not Components V2")
	}
}

func TestKudFightOnlyOneChallengerPays(t *testing.T) {
	b, client, _ := kudTestBot(t)
	b.handleKudGame(client, kudTestMessage(20), "fight", "500")
	g := b.kudGames["fight:20"]
	var wg sync.WaitGroup
	for _, user := range []string{"2", "3"} {
		wg.Go(func() { kudTestClick(t, b, client, g, 0, "join", user) })
	}
	wg.Wait()
	var balance, played int64
	for _, user := range []string{"1", "2", "3"} {
		a, err := b.DB.KudAccount(user)
		if err != nil || a.Held != 0 {
			t.Fatalf("fight left escrow: %+v %v", a, err)
		}
		balance += a.Balance
		played += a.Played
	}
	if !g.done || balance != 30_000 || played != 2 || len(g.entrants) != 2 {
		t.Fatalf("fight settled incorrectly: balance=%d played=%d entrants=%d", balance, played, len(g.entrants))
	}
}

func TestKudLoanConsentAndPlinkoDelivery(t *testing.T) {
	b, client, deliveries := kudTestBot(t)
	m := kudTestMessage(30)
	m.Mentions = []discord.User{{ID: 2, Username: "lender"}}
	b.handleKudGame(client, m, "loan", "<@2> 100")
	loan := b.kudGames["loan:30"]
	if loan == nil {
		t.Fatal("loan request failed")
	}
	kudTestClick(t, b, client, loan, 0, "fund", "3")
	a, _ := b.DB.KudAccount("1")
	if a.Debt != 0 {
		t.Fatal("another user funded the loan")
	}
	kudTestClick(t, b, client, loan, 0, "fund", "2")
	a, _ = b.DB.KudAccount("1")
	if a.Balance != 10_100 || a.Debt != 100 {
		t.Fatalf("funded loan: %+v", a)
	}
	b.handleKudGame(client, kudTestMessage(31), "plinko", "100")
	g := b.kudGames["plinko:31"]
	kudTestClick(t, b, client, g, 0, "join", "2")
	kudTestClick(t, b, client, g, 0, "join", "2")
	if len(g.entrants) != 2 || time.Until(g.expires) > 30*time.Second {
		t.Fatal("duplicate entrant or sliding lobby deadline")
	}
	g.expires = time.Now().Add(-time.Second)
	kudTestClick(t, b, client, g, 0, "join", "3")
	if len(g.entrants) != 2 {
		t.Fatal("late join accepted")
	}
	b.expireKudGame(client, g)
	if len(g.plinkoBalls) != 4 {
		t.Fatalf("100 Kud per entrant should produce 2 balls each, got %d", len(g.plinkoBalls))
	}
	for _, user := range []string{"1", "2"} {
		a, _ := b.DB.KudAccount(user)
		if a.Played != 1 || a.Held != 0 || a.Wagered != 100 || a.Paid != g.plinkoPayouts[user] {
			t.Fatalf("Plinko did not settle: %+v", a)
		}
	}
	deliveries.mu.Lock()
	defer deliveries.mu.Unlock()
	clip, err := gif.DecodeAll(bytes.NewReader(deliveries.clip))
	if err != nil || len(clip.Image) > 64 || clip.Config.Width != 640 {
		t.Fatalf("invalid shared Plinko GIF: %v", err)
	}
	last, _ := json.Marshal(deliveries.payloads[len(deliveries.payloads)-1])
	if !strings.Contains(string(last), "attachment://plinko.gif") || strings.Contains(string(last), `"parse":["users"]`) {
		t.Fatalf("GIF not exposed or mentions enabled: %s", last)
	}
}

func TestHighlowOddsAndInvalidBets(t *testing.T) {
	for card := 2; card <= 14; card++ {
		for _, higher := range []bool{true, false} {
			wins := card - 2
			if higher {
				wins = 14 - card
			}
			factor := highlowFactor(card, higher)
			if wins == 0 && factor != 0 || wins > 0 && (factor*float64(wins)/12 < .969 || factor*float64(wins)/12 > .971) {
				t.Fatal("High-Low odds do not match the calls")
			}
		}
	}
	b, _, _ := kudTestBot(t)
	for _, arg := range []string{"-1", "0", "junk", "1.5", "999999999999999999999999"} {
		if _, err := b.kudBet("1", arg); err == nil {
			t.Fatalf("accepted invalid bet %q", arg)
		}
	}
}

func TestKudFullPlinkoResultsFitDiscord(t *testing.T) {
	g := &kudGame{kind: "plinko", owner: "10000000000000000000", bet: db.MaxKudBet, done: true, result: "**Results**\n"}
	for index := range 32 {
		user := fmt.Sprint(uint64(10_000_000_000_000_000_000) + uint64(index))
		g.entrants = append(g.entrants, kudEntrant{userID: user, name: "player"})
		g.result += fmt.Sprintf("<@%s> · %s · balance %s\n", user, kudMoney(db.MaxKudBet*50), kudMoney(9_000_000_000_000_000))
	}
	g.result += "-# Showing 32 of 64000000 balls. Payouts sum every ball's result.\n"
	g.result += "-# Payouts include the stake. Active loan repayments apply automatically."
	components := g.render()
	body := components[0].(discord.ContainerComponent).Components[0].(discord.TextDisplayComponent).Content
	if length := util.UTF16Len(body); length > 4000 {
		t.Fatalf("full Plinko card exceeds Discord text limit: %d", length)
	}
}

func TestKudPlinkoBallsPayIndependentlyAndKeepResults(t *testing.T) {
	g := &kudGame{bet: 100, entrants: []kudEntrant{{userID: "1", name: "one"}, {userID: "2", name: "two"}}}
	paths := []uint64{0, 63, 4095, 1}
	calls := 0
	g.rollPlinko(func() uint64 {
		path := paths[calls]
		calls++
		return path
	})
	if calls != 4 || len(g.plinkoBalls) != 4 || g.plinkoPayouts["1"] != 2510 || g.plinkoPayouts["2"] != 3000 {
		t.Fatalf("wrong per-ball payouts: calls=%d balls=%d payouts=%v", calls, len(g.plinkoBalls), g.plinkoPayouts)
	}
	for _, ball := range g.plinkoBalls {
		if ball.Multiplier != kudplinko.Payouts[kudplinko.Bin(ball.Steps)] {
			t.Fatal("animation path differs from paid outcome")
		}
	}
	g.rollPlinko(func() uint64 { t.Fatal("retry rerolled the round"); return 0 })
}

func TestKudPlinkoPaysUnrenderedBalls(t *testing.T) {
	g := &kudGame{bet: 2000, entrants: []kudEntrant{{userID: "1", name: "one"}, {userID: "2", name: "two"}, {userID: "3", name: "three"}}}
	calls := 0
	g.rollPlinko(func() uint64 { calls++; return 0 })
	if calls != 120 || len(g.plinkoBalls) != kudplinko.MaxBalls {
		t.Fatalf("wrong counts: draws=%d visible=%d", calls, len(g.plinkoBalls))
	}
	for _, entrant := range g.entrants {
		if g.plinkoPayouts[entrant.userID] != 2000*50 {
			t.Fatalf("unrendered balls were not paid for %s", entrant.userID)
		}
		found := false
		for _, ball := range g.plinkoBalls {
			found = found || strings.HasPrefix(ball.Name, entrant.name+" #")
		}
		if !found {
			t.Fatalf("entrant %s missing from GIF", entrant.userID)
		}
	}
}

func TestKudPlinkoStakeUnitsDefaultAndAll(t *testing.T) {
	for _, test := range []struct {
		arg  string
		want int64
	}{
		{"", 50}, {"50", 50}, {"100", 100}, {"500", 500}, {"all", 10_000}, {"max", 10_000},
		{"1", 0}, {"10", 0}, {"49", 0}, {"75", 0}, {"101", 0},
	} {
		t.Run(test.arg, func(t *testing.T) {
			b, client, _ := kudTestBot(t)
			if _, err := b.DB.AddKudos("1", 7); err != nil {
				t.Fatal(err)
			}
			b.handleKudGame(client, kudTestMessage(42), "plinko", test.arg)
			g := b.kudGames["plinko:42"]
			a, _ := b.DB.KudAccount("1")
			if a.Held != test.want || a.Balance != 10_007-test.want || (g == nil) != (test.want == 0) || g != nil && g.bet != test.want {
				t.Fatalf("incorrect stake: game=%+v account=%+v", g, a)
			}
		})
	}
}
