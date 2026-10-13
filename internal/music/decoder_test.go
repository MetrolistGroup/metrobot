package music

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func localDecoder(t *testing.T, ctx context.Context, duration time.Duration, playback Playback) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	input := filepath.Join(t.TempDir(), "sine.wav")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=1000:duration=1", input).CombinedOutput(); err != nil {
		t.Fatalf("generating local audio: %v %s", err, out)
	}
	args := decoderArgs(input, duration, playback)
	for i, arg := range args {
		if arg == "https,tls,tcp" {
			args[i] = "file" // Only the test uses local input; production keeps its HTTPS whitelist.
		}
	}
	return command(ctx, "ffmpeg", args...)
}

func TestDecoderRealSeekAndVolume(t *testing.T) {
	levels := map[int]float64{}
	for _, volume := range []int{100, 25, 0} {
		cmd := localDecoder(t, context.Background(), 300*time.Millisecond, Playback{Offset: 200 * time.Millisecond, Volume: volume})
		ogg, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		decode := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-f", "f32le", "pipe:1")
		decode.Stdin = bytes.NewReader(ogg)
		pcm, err := decode.Output()
		if err != nil || len(pcm) != 4800*2*4 {
			t.Fatalf("seek should leave 100ms PCM, got %d bytes: %v", len(pcm), err)
		}
		var energy float64
		for i := 0; i < len(pcm); i += 4 {
			sample := float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:])))
			energy += sample * sample
		}
		levels[volume] = math.Sqrt(energy / float64(len(pcm)/4))
	}
	if levels[100] < 0.01 || math.Abs(levels[25]/levels[100]-0.25) > 0.02 || levels[0] > 1e-8 {
		t.Fatalf("volume filter did not attenuate/mute: %v", levels)
	}
}

func TestDecoderBoundsNearEndAndReaps(t *testing.T) {
	for _, remaining := range []time.Duration{time.Millisecond, 20 * time.Millisecond, 21 * time.Millisecond} {
		t.Run(remaining.String(), func(t *testing.T) {
			playback := Playback{Offset: 500 * time.Millisecond, Volume: 100}
			cmd := localDecoder(t, context.Background(), playback.Offset+remaining, playback)
			sink := &fakeSink{}
			if err := runDecoder(context.Background(), cmd, remaining, func(ctx context.Context) error { return ctx.Err() }, sink); err != nil {
				t.Fatal(err)
			}
			want := int32((remaining + 20*time.Millisecond - 1) / (20 * time.Millisecond))
			if sink.frames.Load() != want || cmd.ProcessState == nil {
				t.Fatalf("near-end frames %d, want %d, state %v", sink.frames.Load(), want, cmd.ProcessState)
			}
		})
	}
}

func TestDecoderPausedCancellationReapsBeforeReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := localDecoder(t, ctx, time.Second, Playback{Volume: 100})
	cmd.Args = append([]string{cmd.Args[0], "-re"}, cmd.Args[1:]...)
	paused := make(chan struct{})
	done := make(chan error, 1)
	sink := &fakeSink{}
	go func() {
		done <- runDecoder(ctx, cmd, time.Second, func(ctx context.Context) error {
			close(paused)
			<-ctx.Done()
			return ctx.Err()
		}, sink)
	}()
	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("decoder never reached paused wait")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || cmd.ProcessState == nil || sink.frames.Load() != 0 {
			t.Fatalf("paused cancellation not fully reaped: %v %v", err, cmd.ProcessState)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("paused ffmpeg did not exit")
	}
}

func TestRunValidatesPlaybackAndHandlesReachedEndWithoutTools(t *testing.T) {
	track := Track{ID: "abcdefghijk", Duration: time.Second}
	for _, playback := range []Playback{{Volume: -1}, {Volume: 101}, {Offset: -1}, {Offset: MaxDuration + 1}, {Offset: time.Second + 1}} {
		if err := Run(context.Background(), track, playback, func(Track) { t.Fatal("invalid playback published metadata") }, nil, nil); err == nil {
			t.Fatalf("accepted %+v", playback)
		}
	}
	if err := Run(context.Background(), track, Playback{Offset: time.Second, Volume: 100}, func(Track) {}, nil, nil); err != nil {
		t.Fatalf("volume restart at emitted end should finish without a decoder: %v", err)
	}
}
