package discord

import "testing"

func TestSlotsReturnToPlayer(t *testing.T) {
	total := 0
	for _, symbol := range slotSymbols {
		total += symbol.weight
	}
	var paid, outcomes int64
	for a := range total {
		for b := range total {
			for c := range total {
				rolls := []int{a, b, c}
				paid += slotPayout(spinSlots(func(int) int { r := rolls[0]; rolls = rolls[1:]; return r }), 1000)
				outcomes += 1000
			}
		}
	}
	if rtp := float64(paid) / float64(outcomes); rtp < 0.85 || rtp >= 1 {
		t.Fatalf("slots RTP = %.3f, want house edge", rtp)
	}
}

func TestBlackjackSettlement(t *testing.T) {
	// Cards are 0..51; rank = card%13+1, so 0 is an ace and 12 is a king.
	ace, king, nine, seven, six, five := 0, 12, 8, 6, 5, 4
	cases := []struct {
		player, dealer []int
		want           int64
	}{
		{[]int{ace, king}, []int{nine, seven}, 250},
		{[]int{ace, king}, []int{ace + 13, king + 13}, 100},
		{[]int{nine, seven}, []int{ace, king}, 0},
		{[]int{king, six, nine}, []int{nine, seven}, 0},
		{[]int{king, nine}, []int{king, six, nine}, 200},
		{[]int{king, seven}, []int{king, seven + 13}, 100},
		{[]int{ace, ace + 13, nine}, []int{king, seven}, 200},
		{[]int{five, six}, []int{king, nine}, 0},
	}
	for index, tc := range cases {
		if got, outcome := blackjackPayout(tc.player, tc.dealer, 100); got != tc.want {
			t.Errorf("case %d: payout %d (%s), want %d", index, got, outcome, tc.want)
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
