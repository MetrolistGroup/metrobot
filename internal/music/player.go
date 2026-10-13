package music

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

type Sink interface {
	WriteFrame(context.Context, []byte) error
	Silence(context.Context) error
	Close()
}

type Connector func(context.Context, uint64) (Sink, error)
type Runner func(context.Context, Track, func(Track), func(context.Context) error, Sink) error

type Snapshot struct {
	Channel   uint64
	Current   *Track
	Pending   []Track
	Paused    bool
	Loop      string
	Status    string
	LastError string
}

type session struct {
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	wake        chan struct{}
	channel     uint64
	current     *Track
	pending     []Track
	trackCancel context.CancelFunc
	skipped     bool
	paused      bool
	loop        string
	status      string
	lastError   string
}

type Player struct {
	mu      sync.Mutex
	parent  context.Context
	connect Connector
	run     Runner
	s       *session
	closed  bool
}

func NewPlayer(parent context.Context, connect Connector, run Runner) *Player {
	return &Player{parent: parent, connect: connect, run: run}
}

func finished(s *session) bool {
	if s == nil {
		return true
	}
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func wake(s *session) {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (p *Player) Enqueue(channel uint64, query string) error {
	if channel == 0 {
		return errors.New("Join a normal voice channel first.")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.parent.Err() != nil {
		return errors.New("Music is shutting down.")
	}
	if finished(p.s) {
		ctx, cancel := context.WithCancel(p.parent)
		p.s = &session{ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), channel: channel, loop: "off", status: "starting"}
		go p.work(p.s)
	}
	s := p.s
	if s.ctx.Err() != nil {
		return errors.New("Music is disconnecting; try again shortly.")
	}
	if s.channel != channel {
		return errors.New("Join the bot's voice channel to control music.")
	}
	count := len(s.pending)
	if s.current != nil {
		count++
	}
	if count >= MaxQueue {
		return errors.New("The queue is full (100 tracks including now playing).")
	}
	s.pending = append(s.pending, Track{Query: query})
	wake(s)
	return nil
}

func (p *Player) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.s
	if s == nil {
		return Snapshot{Loop: "off", Status: "disconnected"}
	}
	if finished(s) {
		return Snapshot{Loop: "off", Status: "disconnected", LastError: s.lastError}
	}
	if s.ctx.Err() != nil {
		return Snapshot{Channel: s.channel, Loop: "off", Status: "disconnecting", LastError: s.lastError}
	}
	out := Snapshot{Channel: s.channel, Pending: append([]Track{}, s.pending...), Paused: s.paused, Loop: s.loop, Status: s.status, LastError: s.lastError}
	if s.current != nil {
		t := *s.current
		out.Current = &t
	}
	return out
}

// Control checks channel ownership and changes state atomically, never doing I/O under mu.
func (p *Player) Control(channel uint64, action string, position int, mode string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.s
	if finished(s) || s.ctx.Err() != nil {
		return errors.New("Nothing is playing. Use /play with a query.")
	}
	if channel == 0 || channel != s.channel {
		return errors.New("Join the bot's voice channel to control music.")
	}
	switch action {
	case "resume", "pause":
		if s.current == nil {
			return errors.New("Nothing is playing yet.")
		}
		s.paused = action == "pause"
	case "skip":
		if s.current != nil {
			s.skipped = true
			s.trackCancel()
		} else if len(s.pending) > 0 {
			s.pending = s.pending[1:]
		} else {
			return errors.New("The queue is empty.")
		}
	case "stop":
		s.pending = nil
		s.cancel()
	case "clear":
		s.pending = nil
	case "remove":
		if position < 1 || position > len(s.pending) {
			return errors.New("Position must address a pending track shown by /queue.")
		}
		s.pending = append(s.pending[:position-1], s.pending[position:]...)
	case "shuffle":
		rand.Shuffle(len(s.pending), func(i, j int) { s.pending[i], s.pending[j] = s.pending[j], s.pending[i] })
	case "loop":
		if mode != "off" && mode != "track" && mode != "queue" {
			return errors.New("Choose loop mode: off, track, or queue.")
		}
		s.loop = mode
	default:
		return errors.New("Unknown queue action.")
	}
	wake(s)
	return nil
}

// Disconnect is used for kicks, moves and empty channels; callbacks never wait on the worker.
func (p *Player) Disconnect(channel uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.s != nil && (channel == 0 || p.s.channel == channel) {
		p.s.pending = nil
		p.s.cancel()
	}
}

// Close cancels resolving/decoding and waits for process and connection cleanup.
func (p *Player) Close() {
	p.mu.Lock()
	p.closed = true
	s := p.s
	if s != nil {
		s.pending = nil
		s.cancel()
	}
	p.mu.Unlock()
	if s != nil {
		<-s.done
	}
}

func (p *Player) wait(ctx context.Context, s *session, sink Sink) error {
	stoppedSpeaking := false
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		paused := s.paused
		p.mu.Unlock()
		if !paused {
			return nil
		}
		if !stoppedSpeaking {
			if err := sink.Silence(ctx); err != nil {
				return err
			}
			stoppedSpeaking = true
		}
		select {
		case <-s.wake:
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			s.cancel()
			return errors.New("Disconnected after 10 minutes paused.")
		}
	}
}

func (p *Player) work(s *session) {
	var sink Sink
	defer func() {
		s.cancel()
		if sink != nil {
			sink.Close()
		}
		p.mu.Lock()
		s.current = nil
		s.pending = nil
		s.status = "disconnected"
		close(s.done)
		p.mu.Unlock()
	}()
	for {
		p.mu.Lock()
		if s.ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		if len(s.pending) == 0 {
			s.status = "idle"
			p.mu.Unlock()
			timer := time.NewTimer(time.Minute)
			select {
			case <-s.wake:
				timer.Stop()
				continue
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				// Serialize idle expiry with enqueue so accepted tracks cannot be lost.
				p.mu.Lock()
				if len(s.pending) == 0 {
					s.cancel()
				}
				p.mu.Unlock()
				continue
			}
		}
		track := s.pending[0]
		s.pending = s.pending[1:]
		ctx, cancel := context.WithTimeout(s.ctx, MaxDuration+15*time.Minute)
		s.current = &track
		s.trackCancel = cancel
		s.skipped = false
		s.status = "resolving"
		p.mu.Unlock()

		var err error
		if sink == nil {
			connectCtx, connectCancel := context.WithTimeout(s.ctx, 20*time.Second)
			sink, err = p.connect(connectCtx, s.channel)
			connectCancel()
		}
		if err == nil {
			err = p.run(ctx, track, func(t Track) {
				p.mu.Lock()
				s.current = &t
				s.status = "playing"
				p.mu.Unlock()
			}, func(c context.Context) error { return p.wait(c, s, sink) }, sink)
		}
		cancel()
		if sink != nil && s.ctx.Err() == nil {
			silenceCtx, silenceCancel := context.WithTimeout(s.ctx, 2*time.Second)
			_ = sink.Silence(silenceCtx)
			silenceCancel()
		}
		p.mu.Lock()
		if err != nil && !s.skipped && s.ctx.Err() == nil {
			s.lastError = err.Error()
		}
		if err == nil && !s.skipped && s.ctx.Err() == nil {
			switch s.loop {
			case "track":
				s.pending = append([]Track{*s.current}, s.pending...)
			case "queue":
				s.pending = append(s.pending, *s.current)
			}
		}
		s.current = nil
		s.trackCancel = nil
		p.mu.Unlock()
	}
}
