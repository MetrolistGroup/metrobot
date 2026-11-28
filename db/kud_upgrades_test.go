package db

import (
	"errors"
	"testing"
	"time"
)

func TestKudUpgradePricesCapsAndFunds(t *testing.T) {
	d := kudTestDB(t)
	if _, _, err := d.BuyKudUpgrade("poor", "swift"); !errors.Is(err, ErrKudFunds) {
		t.Fatalf("broke buy err = %v", err)
	}
	if _, err := d.AddKudos("a", 100_000); err != nil {
		t.Fatal(err)
	}
	var spent int64
	for want := int64(1); want <= 6; want++ {
		level, cost, err := d.BuyKudUpgrade("a", "swift")
		if err != nil || level != want || cost != 2_000*want {
			t.Fatalf("level %d: level=%d cost=%d err=%v", want, level, cost, err)
		}
		spent += cost
	}
	if _, _, err := d.BuyKudUpgrade("a", "swift"); !errors.Is(err, ErrKudMaxed) {
		t.Fatalf("seventh level err = %v", err)
	}
	if _, _, err := d.BuyKudUpgrade("a", "nope"); err == nil {
		t.Fatal("unknown upgrade accepted")
	}
	levels, err := d.KudUpgradeLevels("a")
	if err != nil || levels["swift"] != 6 || levels["luck"] != 0 {
		t.Fatalf("levels = %v err=%v", levels, err)
	}
	if a, _ := d.KudAccount("a"); a.Balance != 100_000-spent {
		t.Fatalf("balance = %d, want %d", a.Balance, 100_000-spent)
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
