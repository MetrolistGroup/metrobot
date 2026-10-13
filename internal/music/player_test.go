package music

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSink struct {
	frames   atomic.Int32
	silences atomic.Int32
	closed   atomic.Int32
}

func (s *fakeSink) WriteFrame(context.Context, []byte) error { s.frames.Add(1); return nil }
func (s *fakeSink) Silence(context.Context) error            { s.silences.Add(1); return nil }
func (s *fakeSink) Close()                                   { s.closed.Add(1) }

func awaitTrack(t *testing.T, started <-chan Track) Track {
	t.Helper()
	select {
	case track := <-started:
		return track
	case <-time.After(2 * time.Second):
		t.Fatal("player did not advance")
		return Track{}
	}
}

func testPlayer(t *testing.T) (*Player, *fakeSink, chan Track, chan error) {
	t.Helper()
	sink := &fakeSink{}
	started := make(chan Track, MaxQueue+5)
	release := make(chan error)
	p := NewPlayer(context.Background(), func(context.Context, uint64) (Sink, error) { return sink, nil }, func(ctx context.Context, track Track, info func(Track), wait func(context.Context) error, sink Sink) error {
		track.Title = track.Query
		info(track)
		started <- track
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-release:
			return err
		}
	})
	t.Cleanup(p.Close)
	return p, sink, started, release
}

func TestPlayerOwnershipQueueAndSkipResolving(t *testing.T) {
	p, sink, started, release := testPlayer(t)
	if err := p.Enqueue(1, "one"); err != nil {
		t.Fatal(err)
	}
	awaitTrack(t, started)
	if err := p.Enqueue(2, "hijack"); err == nil {
		t.Fatal("another channel hijacked the session")
	}
	if err := p.Control(2, "stop", 0, ""); err == nil {
		t.Fatal("another channel stopped the session")
	}
	for _, q := range []string{"two", "three", "four"} {
		if err := p.Enqueue(1, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Control(1, "remove", 0, ""); err == nil {
		t.Fatal("zero position accepted")
	}
	if err := p.Control(1, "remove", 2, ""); err != nil {
		t.Fatal(err)
	}
	if got := p.Snapshot(); len(got.Pending) != 2 || got.Pending[1].Query != "four" {
		t.Fatalf("remove: %+v", got)
	}
	// Snapshot does not hand callers mutable queue memory.
	copy := p.Snapshot()
	copy.Pending[0].Query = "mutated"
	copy.Current.Title = "mutated"
	if p.Snapshot().Pending[0].Query != "two" || p.Snapshot().Current.Title != "one" {
		t.Fatal("snapshot aliases player memory")
	}
	if err := p.Control(1, "skip", 0, ""); err != nil {
		t.Fatal(err)
	}
	if got := awaitTrack(t, started); got.Query != "two" {
		t.Fatalf("skip: %+v", got)
	}
	release <- errors.New("source unavailable")
	if got := awaitTrack(t, started); got.Query != "four" {
		t.Fatalf("error advancement: %+v", got)
	}
	if p.Snapshot().LastError != "source unavailable" {
		t.Fatal("playback error lost")
	}
	if err := p.Control(1, "stop", 0, ""); err != nil {
		t.Fatal(err)
	}
	p.Close()
	if sink.closed.Load() != 1 || len(p.Snapshot().Pending) != 0 || p.Snapshot().Current != nil {
		t.Fatal("stop did not reap and clear")
	}
	if err := p.Enqueue(1, "after shutdown"); err == nil {
		t.Fatal("enqueue after shutdown")
	}
}

func TestPlayerQueueLimitAndConcurrentControls(t *testing.T) {
	p, _, started, _ := testPlayer(t)
	_ = p.Enqueue(1, "current")
	awaitTrack(t, started)
	var wg sync.WaitGroup
	for i := 0; i < MaxQueue-1; i++ {
		wg.Go(func() {
			if err := p.Enqueue(1, fmt.Sprint(i)); err != nil {
				t.Error(err)
			}
			_ = p.Snapshot()
		})
	}
	wg.Wait()
	if err := p.Enqueue(1, "overflow"); err == nil || len(p.Snapshot().Pending) != MaxQueue-1 {
		t.Fatal("queue cap failed")
	}
	if err := p.Control(1, "shuffle", 0, ""); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, track := range p.Snapshot().Pending {
		seen[track.Query] = true
	}
	if len(seen) != MaxQueue-1 {
		t.Fatal("shuffle duplicated/lost entries")
	}
	if err := p.Control(1, "clear", 0, ""); err != nil || len(p.Snapshot().Pending) != 0 {
		t.Fatal("clear failed")
	}
}

func TestPlayerLoopSkipAndDisconnect(t *testing.T) {
	p, _, started, release := testPlayer(t)
	_ = p.Enqueue(1, "one")
	awaitTrack(t, started)
	_ = p.Enqueue(1, "two")
	if err := p.Control(1, "loop", 0, "track"); err != nil {
		t.Fatal(err)
	}
	release <- nil
	if got := awaitTrack(t, started); got.Query != "one" {
		t.Fatal("track loop failed")
	}
	_ = p.Control(1, "skip", 0, "")
	if got := awaitTrack(t, started); got.Query != "two" {
		t.Fatal("skip repeated the skipped track")
	}
	_ = p.Control(1, "loop", 0, "queue")
	_ = p.Enqueue(1, "three")
	release <- nil
	if got := awaitTrack(t, started); got.Query != "three" {
		t.Fatal("queue loop order failed")
	}
	if got := p.Snapshot().Pending; len(got) != 1 || got[0].Query != "two" {
		t.Fatal("queue loop lost current")
	}
	p.Disconnect(2)
	if p.Snapshot().Current == nil {
		t.Fatal("other channel disconnected player")
	}
	p.Disconnect(1)
	p.Close()
	if p.Snapshot().Channel != 0 {
		t.Fatal("disconnect not reaped")
	}
}

func TestPauseBackpressureAndStopUnblocks(t *testing.T) {
	sink := &fakeSink{}
	ready := make(chan struct{})
	begin := make(chan struct{})
	p := NewPlayer(context.Background(), func(context.Context, uint64) (Sink, error) { return sink, nil }, func(ctx context.Context, track Track, info func(Track), wait func(context.Context) error, sink Sink) error {
		info(track)
		close(ready)
		select {
		case <-begin:
		case <-ctx.Done():
			return ctx.Err()
		}
		for {
			if err := wait(ctx); err != nil {
				return err
			}
			if err := sink.WriteFrame(ctx, []byte{0xfc}); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	t.Cleanup(p.Close)
	_ = p.Enqueue(1, "one")
	<-ready
	if err := p.Control(1, "pause", 0, ""); err != nil {
		t.Fatal(err)
	}
	close(begin)
	time.Sleep(30 * time.Millisecond)
	if sink.frames.Load() != 0 {
		t.Fatal("paused producer consumed audio")
	}
	_ = p.Control(1, "resume", 0, "")
	deadline := time.Now().Add(time.Second)
	for sink.frames.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if sink.frames.Load() == 0 {
		t.Fatal("resume did not release backpressure")
	}
	_ = p.Control(1, "pause", 0, "")
	p.Close()
	if sink.closed.Load() != 1 {
		t.Fatal("paused shutdown did not reap")
	}
}

func TestStopAllowsNewChannelOnlyAfterCleanup(t *testing.T) {
	closed := make(chan struct{})
	sink := &blockingCloseSink{fakeSink: &fakeSink{}, gate: closed}
	started := make(chan struct{}, 1)
	p := NewPlayer(context.Background(), func(context.Context, uint64) (Sink, error) { return sink, nil }, func(ctx context.Context, track Track, info func(Track), wait func(context.Context) error, sink Sink) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	t.Cleanup(p.Close)
	_ = p.Enqueue(1, "one")
	<-started
	_ = p.Control(1, "stop", 0, "")
	if err := p.Enqueue(2, "two"); err == nil {
		t.Fatal("new connection allowed while old close was pending")
	}
	close(closed)
	deadline := time.Now().Add(time.Second)
	for p.Snapshot().Channel != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := p.Enqueue(2, "two"); err != nil {
		t.Fatal(err)
	}
	<-started
}

type blockingCloseSink struct {
	*fakeSink
	gate chan struct{}
}

func (s *blockingCloseSink) Close() { <-s.gate; s.fakeSink.Close() }
