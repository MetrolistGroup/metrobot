package db

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestKudUpgradePricesCapsAndFunds(t *testing.T) {
	d := kudTestDB(t)
	if _, _, err := d.BuyKudUpgrade("poor", "swift", 1, 10_000); !errors.Is(err, ErrKudFunds) {
		t.Fatalf("broke buy err = %v", err)
	}
	if _, err := d.AddKudos("a", 2_000_000_000); err != nil {
		t.Fatal(err)
	}
	var spent int64
	for key, base := range map[string]int64{"swift": 10_000, "luck": 15_000} {
		for want := int64(1); want <= 50; want++ {
			price := base * want * want
			level, cost, err := d.BuyKudUpgrade("a", key, want, price)
			if err != nil || level != want || cost != price {
				t.Fatalf("%s level %d: level=%d cost=%d err=%v", key, want, level, cost, err)
			}
			spent += cost
		}
		if _, _, err := d.BuyKudUpgrade("a", key, 51, base*51*51); !errors.Is(err, ErrKudMaxed) {
			t.Fatalf("%s fifty-first level err = %v", key, err)
		}
	}
	if _, _, err := d.BuyKudUpgrade("a", "nope", 1, 10_000); err == nil {
		t.Fatal("unknown upgrade accepted")
	}
	levels, err := d.KudUpgradeLevels("a")
	if err != nil || levels["swift"] != 50 || levels["luck"] != 50 {
		t.Fatalf("levels = %v err=%v", levels, err)
	}
	if a, _ := d.KudAccount("a"); a.Balance != 2_000_000_000-spent {
		t.Fatalf("balance = %d, want %d", a.Balance, 2_000_000_000-spent)
	}
}

func TestKudUpgradeRejectsChangedQuotes(t *testing.T) {
	d := kudTestDB(t)
	if _, err := d.AddKudos("a", 100_000); err != nil {
		t.Fatal(err)
	}
	for _, quote := range [][2]int64{{1, 2_000}, {2, 40_000}, {0, 0}} {
		if _, _, err := d.BuyKudUpgrade("a", "swift", quote[0], quote[1]); !errors.Is(err, ErrKudQuoteChanged) {
			t.Fatalf("changed quote %v accepted: %v", quote, err)
		}
	}
	if _, _, err := d.BuyKudUpgrade("a", "swift", 1, 10_000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.BuyKudUpgrade("a", "swift", 1, 10_000); !errors.Is(err, ErrKudQuoteChanged) {
		t.Fatalf("repeated confirmation accepted: %v", err)
	}
	if a, _ := d.KudAccount("a"); a.Balance != 90_000 {
		t.Fatalf("rejected quotes spent Kud: %d", a.Balance)
	}
}

func TestKudConcurrentConfirmationsChargeOnce(t *testing.T) {
	d := kudTestDB(t)
	if _, err := d.AddKudos("a", 100_000); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errorsFound := make(chan error, 12)
	for range 12 {
		workers.Go(func() {
			_, _, err := d.BuyKudUpgrade("a", "swift", 1, 10_000)
			errorsFound <- err
		})
	}
	workers.Wait()
	close(errorsFound)
	var purchased int
	for err := range errorsFound {
		if err == nil {
			purchased++
		} else if !errors.Is(err, ErrKudQuoteChanged) {
			t.Fatal(err)
		}
	}
	levels, err := d.KudUpgradeLevels("a")
	if err != nil || purchased != 1 || levels["swift"] != 1 {
		t.Fatalf("confirmations bought %d times, levels %v: %v", purchased, levels, err)
	}
	if a, _ := d.KudAccount("a"); a.Balance != 90_000 {
		t.Fatalf("confirmation charged more than once: %d", a.Balance)
	}
}

func TestCookieOncePerDayAndNoSelf(t *testing.T) {
	d := kudTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	total, _, ok, err := d.GiveCookie("a", "b", now)
	if err != nil || !ok || total != 1 {
		t.Fatalf("first gift total=%d ok=%v err=%v", total, ok, err)
	}
	_, next, ok, err := d.GiveCookie("a", "c", now.Add(time.Hour))
	if err != nil || ok || !next.Equal(now.Add(CookieCooldown)) {
		t.Fatalf("second gift same day ok=%v next=%v err=%v", ok, next, err)
	}
	if total, _, ok, _ := d.GiveCookie("a", "b", now.Add(CookieCooldown)); !ok || total != 2 {
		t.Fatalf("gift after 24h total=%d ok=%v", total, ok)
	}
	if _, _, _, err := d.GiveCookie("c", "c", now); err == nil {
		t.Fatal("self gift accepted")
	}
}
