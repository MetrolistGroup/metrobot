package discord

import (
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
)

func TestKudRateLimitLayers(t *testing.T) {
	b, client, _ := kudTestBot(t)
	m := kudTestMessage(40)
	b.handleKudGame(client, m, "mines", "10")
	if b.kudGames["mines:40"] == nil {
		t.Fatal("first mines round did not start")
	}
	// The blanket layer blocks a different game straight after any stake.
	b.handleKudGame(client, kudTestMessage(41), "highlow", "10")
	if b.kudGames["highlow:41"] != nil {
		t.Fatal("blanket cooldown did not block highlow")
	}
	// Once the blanket expires, the mines layer still blocks mines.
	b.setCooldown("kud", "1", 0)
	b.handleKudGame(client, kudTestMessage(42), "mines", "10")
	if b.kudGames["mines:42"] != nil {
		t.Fatal("game cooldown did not block a second mines round")
	}
}

func TestSwiftShortensBothLayers(t *testing.T) {
	b, _, _ := kudTestBot(t)
	if _, err := b.DB.AddKudos("1", 2_000_000_000); err != nil {
		t.Fatal(err)
	}
	if got := b.kudCooldownFor("1", 10*time.Second); got != 10*time.Second {
		t.Fatalf("no upgrade cooldown = %s", got)
	}
	for level := int64(1); level <= 50; level++ {
		if _, _, err := b.DB.BuyKudUpgrade("1", "swift", level, 10_000*level*level); err != nil {
			t.Fatal(err)
		}
		if level == 1 {
			if got := b.kudCooldownFor("1", 10*time.Second); got != 9980*time.Millisecond {
				t.Fatalf("first swift level cooldown = %s, want 9.98s", got)
			}
		}
	}
	if got := b.kudCooldownFor("1", kudBlanketCooldown); got != 4500*time.Millisecond {
		t.Fatalf("maxed swift blanket cooldown = %s, want 4.5s", got)
	}
	if got := b.kudCooldownFor("1", 10*time.Second); got != 9*time.Second {
		t.Fatalf("maxed swift cooldown = %s, want 9s", got)
	}
}

func TestLuckPreservesFractionalPercentages(t *testing.T) {
	for _, test := range []struct {
		debt  bool
		level int64
		want  int
	}{
		{false, 0, 0}, {false, 1, 4}, {false, 20, 80}, {false, 50, 200},
		{true, 0, 200}, {true, 5, 220}, {true, 20, 280}, {true, 50, 400},
	} {
		if got := kudLuck(test.debt, test.level); got != test.want {
			t.Fatalf("debt=%v level=%d luck=%d basis points, want %d", test.debt, test.level, got, test.want)
		}
	}
}

func TestCookieCommandRoutesAndValidates(t *testing.T) {
	b, client, _ := kudTestBot(t)
	m := kudTestMessage(50)
	b.handleCookie(client, m, "1")
	if _, next, ok, _ := b.DB.GiveCookie("1", "9", time.Now()); !ok || next.IsZero() {
		t.Fatal("usage without a mention spent the giver's daily cookie")
	}
	m.Mentions = []discord.User{{ID: 2, Username: "friend"}}
	b.handleCookie(client, m, "1")
	if _, _, ok, _ := b.DB.GiveCookie("1", "3", time.Now()); ok {
		t.Fatal("mention did not spend the giver's daily cookie")
	}
}
