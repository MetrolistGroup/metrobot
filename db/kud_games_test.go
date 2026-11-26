package db

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func kudTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "kud.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestKudEscrowConcurrentSettlementAndRecovery(t *testing.T) {
	d := kudTestDB(t)
	for _, user := range []string{"a", "b"} {
		if _, err := d.AddKudos(user, 1000); err != nil {
			t.Fatal(err)
		}
		if err := d.HoldKud("fight", "fight", user, 100); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.HoldKud("fight", "fight", "a", 100); err == nil {
		t.Fatal("duplicate hold accepted")
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := d.SettleKud("fight", map[string]int64{"a": 200, "b": 0})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrKudClosed) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("settled %d times", successes)
	}
	a, _ := d.KudAccount("a")
	b, _ := d.KudAccount("b")
	if a.Balance != 1100 || b.Balance != 900 || a.Played != 1 || b.Played != 1 || a.Wins != 1 || b.Wins != 0 || a.Wagered != 100 || a.Paid != 200 || a.Held != 0 {
		t.Fatalf("accounts: a=%+v b=%+v", a, b)
	}
	if err := d.HoldKud("unfinished", "mines", "a", 700); err != nil {
		t.Fatal(err)
	}
	if err := d.RecoverKud(); err != nil {
		t.Fatal(err)
	}
	if err := d.RecoverKud(); err != nil {
		t.Fatal(err)
	}
	a, _ = d.KudAccount("a")
	if a.Balance != 1100 || a.Played != 1 || a.Held != 0 {
		t.Fatalf("recovery changed balance or stats: %+v", a)
	}
}

func TestKudLoanRepaymentAndRefundExclusions(t *testing.T) {
	d := kudTestDB(t)
	if _, err := d.AddKudos("lender", 1000); err != nil {
		t.Fatal(err)
	}
	if err := d.FundKudLoan("lender", "borrower", 101); err != nil {
		t.Fatal(err)
	}
	if err := d.FundKudLoan("lender", "borrower", 100); err == nil {
		t.Fatal("second active loan allowed")
	}
	if err := d.HoldKud("refund", "fight", "borrower", 100); err != nil {
		t.Fatal(err)
	}
	if err := d.RefundKud("refund"); err != nil {
		t.Fatal(err)
	}
	a, _ := d.KudAccount("borrower")
	if a.Balance != 101 || a.Debt != 101 || a.Played != 0 {
		t.Fatalf("loan or refund taxed: %+v", a)
	}
	for range 5 {
		if _, err := d.AddKudos("borrower", 1); err != nil {
			t.Fatal(err)
		}
	}
	a, _ = d.KudAccount("borrower")
	lender, _ := d.Kudos("lender")
	if a.Balance != 105 || a.Debt != 100 || lender != 900 {
		t.Fatalf("small payouts bypassed tax: %+v lender=%d", a, lender)
	}
	if _, err := d.AddKudos("borrower", 1000); err != nil {
		t.Fatal(err)
	}
	a, _ = d.KudAccount("borrower")
	lender, _ = d.Kudos("lender")
	if a.Balance != 1005 || a.Debt != 0 || lender != 1000 {
		t.Fatalf("final payment exceeded debt: %+v lender=%d", a, lender)
	}
	if err := d.FundKudLoan("lender", "borrower", 100); err != nil {
		t.Fatal(err)
	}
	amount, _, balance, _, ok, err := d.ClaimDaily("borrower", 0, time.Now())
	a, _ = d.KudAccount("borrower")
	if err != nil || !ok || a.Debt != 0 || balance != 1005+amount {
		t.Fatalf("daily repayment: %+v amount=%d balance=%d %v", a, amount, balance, err)
	}
}

func TestKudFailedSettlementRollsBackEveryEntrant(t *testing.T) {
	d := kudTestDB(t)
	for _, user := range []string{"a", "b"} {
		d.AddKudos(user, 1000)
		if err := d.HoldKud("round", "plinko", user, 100); err != nil {
			t.Fatal(err)
		}
	}
	for index, payouts := range []map[string]int64{{"a": 200}, {"a": 200, "b": -1}, {"a": 200, "stranger": 100}, {"a": 200, "b": 1 << 62}} {
		if _, err := d.SettleKud("round", payouts); err == nil {
			t.Fatalf("accepted invalid settlement %d", index)
		}
		for _, user := range []string{"a", "b"} {
			a, _ := d.KudAccount(user)
			if a.Balance != 900 || a.Held != 100 || a.Played != 0 {
				t.Fatalf("partial settlement %d %s: %+v", index, user, a)
			}
		}
	}
	if _, err := d.SettleKud("round", map[string]int64{"a": 200, "b": 0}); err != nil {
		t.Fatal(err)
	}
	for _, amount := range []int64{-1, 0, MaxKudBet + 1, 1 << 62} {
		if err := d.HoldKud(fmt.Sprint(amount), "mines", "a", amount); err == nil {
			t.Fatalf("accepted hold %d", amount)
		}
	}
}

func TestKudDoubleHoldAndTaxedSettlement(t *testing.T) {
	d := kudTestDB(t)
	if _, err := d.AddKudos("lender", 1000); err != nil {
		t.Fatal(err)
	}
	if err := d.FundKudLoan("lender", "player", 100); err != nil {
		t.Fatal(err)
	}
	if err := d.HoldKud("blackjack", "bj", "player", 10); err != nil {
		t.Fatal(err)
	}
	if err := d.DoubleKudHold("blackjack", "player"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SettleKud("blackjack", map[string]int64{"player": 44}); err != nil {
		t.Fatal(err)
	}
	a, _ := d.KudAccount("player")
	if a.Balance != 116 || a.Debt != 92 || a.Played != 1 || a.Wins != 1 || a.Wagered != 20 || a.Paid != 44 || a.Held != 0 {
		t.Fatalf("double down or tax accounting: %+v", a)
	}
}
