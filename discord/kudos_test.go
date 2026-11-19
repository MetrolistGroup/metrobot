package discord

import (
	"testing"
	"time"

	"github.com/MetrolistGroup/metrobot/db"
)

func TestSlotsReturnToPlayer(t *testing.T) {
	n := len(slotSymbols)
	var paid, staked int64
	for a := range n {
		for b := range n {
			for c := range n {
				paid += slotWinnings([3]int{a, b, c}, 1000)
				staked += 1000
			}
		}
	}
	// Sigma's slots are deliberately player-favored (gated by the 60s cooldown); winnings exceed stake overall.
	if rtp := float64(paid) / float64(staked); rtp < 1.2 || rtp > 2.5 {
		t.Fatalf("slots RTP = %.3f, expected Sigma's player-favored range", rtp)
	}
	if w := slotWinnings([3]int{0, 0, 0}, 100); w != int64(100*slotSymbols[0].reward*float64(n*n)*0.95) {
		t.Fatalf("three of a kind payout = %d", w)
	}
	if w := slotWinnings([3]int{5, 5, 1}, 100); w != int64(100*slotSymbols[5].reward*float64(n/2)*0.9) {
		t.Fatalf("pair payout = %d", w)
	}
	if w := slotWinnings([3]int{0, 1, 2}, 100); w != 0 {
		t.Fatalf("no-match payout = %d", w)
	}
}

func TestBlackjackSettlement(t *testing.T) {
	// Cards are 0..51; rank = card%13+1, so 0 is an ace and 12 is a king.
	ace, king, nine, seven, six, five := 0, 12, 8, 6, 5, 4
	cases := []struct {
		name           string
		player, dealer []int
		want           int64 // amount returned including the 100 stake already taken
	}{
		{"even-money win", []int{nine, seven, five}, []int{king, seven}, 200},
		{"natural blackjack 6:5", []int{ace, king}, []int{nine, seven}, 220},
		{"both naturals push", []int{ace, king}, []int{ace + 13, king + 13}, 100},
		{"lose", []int{nine, seven}, []int{king, ace}, 0},
		{"player bust", []int{king, six, nine}, []int{nine, seven}, 0},
		{"dealer bust", []int{king, nine}, []int{king, six, nine}, 200},
		{"push", []int{king, seven}, []int{king, seven + 13}, 100},
		{"soft ace win", []int{ace, ace + 13, nine}, []int{king, seven}, 200},
	}
	for _, tc := range cases {
		if got, outcome := blackjackPayout(tc.player, tc.dealer, 100); got != tc.want {
			t.Errorf("%s: payout %d (%s), want %d", tc.name, got, outcome, tc.want)
		}
	}
}

func TestDealerHitsSoft17(t *testing.T) {
	ace, six, king, seven := 0, 5, 12, 6
	if !dealerShouldHit([]int{ace, six}) { // soft 17
		t.Error("dealer should hit soft 17")
	}
	if dealerShouldHit([]int{king, seven}) { // hard 17
		t.Error("dealer should stand on hard 17")
	}
}

func TestDailyAmount(t *testing.T) {
	for _, tc := range []struct {
		streak, random, want int64
	}{
		{1, 0, 5001}, {1, 999, 6000}, {2, 0, 8202}, {10, 0, 21010}, {50, 500, 21550},
	} {
		if got := db.DailyAmount(tc.streak, tc.random); got != tc.want {
			t.Errorf("DailyAmount(%d,%d) = %d, want %d", tc.streak, tc.random, got, tc.want)
		}
	}
}

func TestWolframLocal(t *testing.T) {
	for query, want := range map[string]string{"2+2": "4", "1/3": "1/3 ≈ 0.3333333333", "2^10": "1024"} {
		if got, ok := wolframLocal(query); !ok || got != want {
			t.Errorf("%q = %q %v, want %q", query, got, ok, want)
		}
	}
	if _, ok := wolframLocal("population of france"); ok {
		t.Error("non-math query answered locally")
	}
}

func TestCooldownCheckDoesNotArm(t *testing.T) {
	b := &Bot{}
	// A rejected spin only calls onCooldown (never setCooldown), so checking must never arm a cooldown —
	// otherwise a bet cancelled for no money would still block the next spin for 60s.
	for range 3 {
		if _, on := b.onCooldown("slots", "broke"); on {
			t.Fatal("checking the cooldown armed it")
		}
	}
	b.setCooldown("slots", "spent", time.Minute)
	if _, on := b.onCooldown("slots", "spent"); !on {
		t.Fatal("setCooldown did not arm a cooldown")
	}
}
