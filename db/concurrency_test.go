package db

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentWritesWaitForLock(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "concurrency.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	const n = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	numbers := make(chan int64, n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			<-start
			c, err := d.CreateCase("discord", "warn", fmt.Sprint(i), "mod", "reason")
			if err != nil {
				errs <- fmt.Errorf("CreateCase: %w", err)
				return
			}
			numbers <- c.CaseNumber
		}(i)
		go func(i int) {
			defer wg.Done()
			<-start
			if _, _, _, _, _, err := d.ClaimDaily(fmt.Sprint(i%4), 0, time.Unix(1_000_000, 0)); err != nil {
				errs <- fmt.Errorf("ClaimDaily: %w", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	close(numbers)
	for err := range errs {
		t.Error(err)
	}
	seen := map[int64]bool{}
	for number := range numbers {
		if seen[number] {
			t.Errorf("duplicate case number %d", number)
		}
		seen[number] = true
	}
	if len(seen) != n {
		t.Fatalf("created %d cases, want %d", len(seen), n)
	}
	for i := 0; i < 4; i++ {
		balance, err := d.Kudos(fmt.Sprint(i))
		if err != nil || balance != DailyAmount(1, 0) {
			t.Errorf("user %d balance = %d, %v; want one daily claim of %d", i, balance, err, DailyAmount(1, 0))
		}
	}
}
