package music

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type playbackAttempt struct {
	track     Track
	playback  Playback
	info      func(Track)
	emit      chan int
	wrote     chan error
	finish    chan error
	cancelled chan struct{}
	reap      chan struct{}
}

func restartPlayer(t *testing.T, duration time.Duration, sink Sink) (*Player, <-chan *playbackAttempt) {
	t.Helper()
	started := make(chan *playbackAttempt, 10)
	shutdown := make(chan struct{})
	p := NewPlayer(context.Background(), func(context.Context, uint64) (Sink, error) { return sink, nil }, func(ctx context.Context, track Track, playback Playback, info func(Track), wait func(context.Context) error, sink Sink) error {
		if duration > 0 {
			if track.ID == "" {
				track.ID, track.Title, track.Duration = "abcdefghijk", track.Query, duration
			}
			info(track)
		}
		a := &playbackAttempt{track: track, playback: playback, info: info, emit: make(chan int, 1), wrote: make(chan error, 1), finish: make(chan error, 1), cancelled: make(chan struct{}), reap: make(chan struct{})}
		started <- a
		for {
			select {
			case <-ctx.Done():
				close(a.cancelled)
				select {
				case <-a.reap:
				case <-shutdown:
				}
				// A killed decoder need not return context.Canceled; controls must suppress tool errors too.
				return errors.New("decoder killed")
			case err := <-a.finish:
				return err
			case n := <-a.emit:
				var err error
				for i := 0; i < n; i++ {
					if err = wait(ctx); err != nil {
						break
					}
					if err = sink.WriteFrame(ctx, []byte{0xfc}); err != nil {
						break
					}
				}
				a.wrote <- err
			}
		}
	})
	t.Cleanup(func() { close(shutdown); p.Close() })
	return p, started
}

func nextAttempt(t *testing.T, started <-chan *playbackAttempt) *playbackAttempt {
	t.Helper()
	select {
	case a := <-started:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("decoder did not start")
		return nil
	}
}

func waitCancelled(t *testing.T, a *playbackAttempt) {
	t.Helper()
	select {
	case <-a.cancelled:
	case <-time.After(time.Second):
		t.Fatal("decoder was not cancelled")
	}
}

func emitFrames(t *testing.T, a *playbackAttempt, n int) {
	t.Helper()
	a.emit <- n
	select {
	case err := <-a.wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("frames not emitted")
	}
}

func control(t *testing.T, p *Player, action string, value int) {
	t.Helper()
	if err := p.Control(1, action, value, ""); err != nil {
		t.Fatal(err)
	}
}

func TestSeekVolumeRestartKeepsPauseAndQueue(t *testing.T) {
	sink := &fakeSink{}
	p, started := restartPlayer(t, 10*time.Second, sink)
	_ = p.Enqueue(1, "one")
	a := nextAttempt(t, started)
	if a.playback.Volume != 100 || p.Snapshot().Volume != 100 {
		t.Fatal("volume did not default to 100")
	}
	_ = p.Enqueue(1, "two")
	_ = p.Enqueue(1, "three")
	_ = p.Control(1, "loop", 0, "queue")
	emitFrames(t, a, 3)
	control(t, p, "pause", 0)
	p.mu.Lock()
	pauseUntil := p.s.pauseUntil
	p.mu.Unlock()
	time.Sleep(25 * time.Millisecond)
	if p.Snapshot().Elapsed != 60*time.Millisecond {
		t.Fatal("wall time advanced playback position")
	}
	control(t, p, "seek", 1)
	waitCancelled(t, a)
	a.info(Track{Query: "stale", Title: "stale", Duration: time.Hour})
	if p.Snapshot().Current.Title != "one" {
		t.Fatal("cancelled metadata overwrote current track")
	}
	select {
	case <-started:
		t.Fatal("decoder restarted before previous child was reaped")
	default:
	}
	control(t, p, "shuffle", 0)
	close(a.reap)
	b := nextAttempt(t, started)
	if b.track != a.track || b.playback.Offset != time.Second || b.playback.Volume != 100 || !p.Snapshot().Paused || len(p.Snapshot().Pending) != 2 {
		t.Fatalf("seek changed metadata, pause or queue: %+v %+v", b, p.Snapshot())
	}
	b.emit <- 1
	control(t, p, "volume", 0)
	waitCancelled(t, b)
	close(b.reap)
	c := nextAttempt(t, started)
	if c.playback.Offset != time.Second || c.playback.Volume != 0 || !p.Snapshot().Paused || sink.frames.Load() != 3 {
		t.Fatalf("paused volume restart emitted/advanced audio: %+v %+v", c.playback, p.Snapshot())
	}
	p.mu.Lock()
	unchangedPause := p.s.pauseUntil == pauseUntil
	p.mu.Unlock()
	if !unchangedPause {
		t.Fatal("restart extended the pause timeout")
	}
	c.emit <- 1
	control(t, p, "resume", 0)
	select {
	case err := <-c.wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("restart lost resume")
	}
	control(t, p, "volume", 75)
	waitCancelled(t, c)
	close(c.reap)
	d := nextAttempt(t, started)
	if d.playback.Offset != time.Second+20*time.Millisecond || d.playback.Volume != 75 || p.Snapshot().LastError != "" {
		t.Fatalf("volume did not use emitted offset: %+v %+v", d.playback, p.Snapshot())
	}
}

func TestPlaybackControlsDuringLookup(t *testing.T) {
	p, started := restartPlayer(t, 0, &fakeSink{})
	_ = p.Enqueue(1, "one")
	a := nextAttempt(t, started)
	if err := p.Control(1, "seek", 0, ""); err == nil {
		t.Fatal("seek accepted before metadata")
	}
	control(t, p, "volume", 25)
	waitCancelled(t, a)
	metadata := Track{Query: "one", ID: "abcdefghijk", Title: "one", Duration: 10 * time.Second}
	a.info(metadata)
	if p.Snapshot().Current.ID != "" {
		t.Fatal("cancelled lookup published metadata")
	}
	close(a.reap)
	b := nextAttempt(t, started)
	if b.playback.Volume != 25 || b.track.ID != "" {
		t.Fatal("volume during lookup lost query or setting")
	}
	b.info(metadata)
	emitFrames(t, b, 1)
	control(t, p, "seek", 1)
}

func TestPlaybackNearEndOffsetDoesNotExceedDuration(t *testing.T) {
	duration := 2*time.Second + time.Millisecond
	p, started := restartPlayer(t, duration, &fakeSink{})
	_ = p.Enqueue(1, "one")
	a := nextAttempt(t, started)
	emitFrames(t, a, 1)
	control(t, p, "seek", 2)
	waitCancelled(t, a)
	close(a.reap)
	b := nextAttempt(t, started)
	emitFrames(t, b, 1)
	if p.Snapshot().Elapsed != duration {
		t.Fatal("near-end padded frame exceeded metadata duration")
	}
	control(t, p, "volume", 50)
	waitCancelled(t, b)
	close(b.reap)
	if nextAttempt(t, started).playback.Offset != duration {
		t.Fatal("near-end volume restart exceeded duration")
	}
}

func TestPlaybackControlBoundsAndUnresolvedStart(t *testing.T) {
	p, started := restartPlayer(t, 10*time.Second, &fakeSink{})
	if p.Snapshot().Volume != 100 {
		t.Fatal("disconnected default volume")
	}
	_ = p.Enqueue(1, "one")
	a := nextAttempt(t, started)
	if err := p.Control(1, "seek", 0, ""); err == nil {
		t.Fatal("seek accepted before the first frame")
	}
	for _, action := range []string{"seek", "volume"} {
		if err := p.Control(2, action, 0, ""); err == nil {
			t.Fatal("another channel controlled playback")
		}
	}
	emitFrames(t, a, 1)
	for _, test := range []struct {
		action string
		value  int
	}{{"seek", -1}, {"seek", 10}, {"seek", 10801}, {"seek", int(^uint(0) >> 1)}, {"volume", -1}, {"volume", 101}} {
		if err := p.Control(1, test.action, test.value, ""); err == nil {
			t.Fatalf("accepted %s %d", test.action, test.value)
		}
	}
	control(t, p, "volume", 100) // No-op must not cancel the decoder.
	select {
	case <-a.cancelled:
		t.Fatal("unchanged volume restarted decoder")
	default:
	}
	control(t, p, "seek", 0)
	waitCancelled(t, a)
	if err := p.Control(1, "seek", 2, ""); err == nil {
		t.Fatal("seek accepted during unresolved restart")
	}
	close(a.reap)
	b := nextAttempt(t, started)
	if b.playback.Offset != 0 || p.Snapshot().Elapsed != 0 {
		t.Fatal("seek to zero failed")
	}
	b.finish <- nil
	deadline := time.Now().Add(time.Second)
	for p.Snapshot().Current != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	control(t, p, "volume", 0)
	_ = p.Enqueue(1, "two")
	if nextAttempt(t, started).playback.Volume != 0 {
		t.Fatal("idle volume did not apply to next track")
	}
}

func TestSkipStopWinDuringRestartCleanup(t *testing.T) {
	for _, restart := range []string{"seek", "volume"} {
		for _, action := range []string{"skip", "stop"} {
			t.Run(restart+"/"+action, func(t *testing.T) {
				p, started := restartPlayer(t, 10*time.Second, &fakeSink{})
				_ = p.Enqueue(1, "one")
				a := nextAttempt(t, started)
				emitFrames(t, a, 2)
				_ = p.Enqueue(1, "two")
				_ = p.Control(1, "loop", 0, "track")
				control(t, p, restart, 1)
				waitCancelled(t, a)
				control(t, p, action, 0)
				close(a.reap)
				if action == "skip" {
					b := nextAttempt(t, started)
					a.info(Track{Title: "late callback", Duration: time.Hour})
					if b.track.Query != "two" || b.playback.Offset != 0 || p.Snapshot().Current.Query != "two" || len(p.Snapshot().Pending) != 0 {
						t.Fatalf("skip lost to restart/loop/stale callback: %+v", p.Snapshot())
					}
				} else {
					p.Close()
					select {
					case <-started:
						t.Fatal("stop restarted or advanced track")
					default:
					}
				}
				if p.Snapshot().LastError != "" {
					t.Fatal("cancellation reported as source failure")
				}
			})
		}
	}
}

type inFlightSink struct {
	fakeSink
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *inFlightSink) WriteFrame(ctx context.Context, frame []byte) error {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.fakeSink.WriteFrame(ctx, frame)
}

func TestVolumeRestartsAfterInFlightSuccessfulFrame(t *testing.T) {
	sink := &inFlightSink{entered: make(chan struct{}), release: make(chan struct{})}
	p, started := restartPlayer(t, 10*time.Second, sink)
	_ = p.Enqueue(1, "one")
	a := nextAttempt(t, started)
	a.emit <- 1
	<-sink.entered
	control(t, p, "volume", 50)
	close(sink.release)
	waitCancelled(t, a)
	close(a.reap)
	if nextAttempt(t, started).playback.Offset != 20*time.Millisecond {
		t.Fatal("volume used offset from before in-flight transport write")
	}
}
