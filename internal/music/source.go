// Package music provides bounded, ephemeral YouTube playback without a gateway dependency.
package music

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxQueue      = 100
	MaxQuery      = 200
	MaxDuration   = 3 * time.Hour
	lookupTimeout = 30 * time.Second
)

var videoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

type Track struct {
	Query    string
	ID       string
	Title    string
	Duration time.Duration
}

func (t Track) Label() string {
	if t.Title != "" {
		return t.Title
	}
	return t.Query
}

// NormalizeQuery never passes user URLs to a generic extractor.
func NormalizeQuery(input string) (string, error) {
	q := strings.TrimSpace(input)
	if q == "" || !utf8.ValidString(q) || len(q) > MaxQuery || strings.IndexFunc(q, unicode.IsControl) >= 0 {
		return "", errors.New("Use a YouTube video URL or a search of 1–200 bytes.")
	}
	if strings.Contains(q, "://") || strings.HasPrefix(q, "www.") || strings.HasPrefix(q, "//") {
		u, err := url.Parse(q)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
			return "", errors.New("Only HTTPS YouTube video URLs are supported.")
		}
		var id string
		switch strings.ToLower(u.Host) {
		case "youtu.be":
			id = strings.TrimPrefix(u.Path, "/")
		case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com":
			switch {
			case u.Path == "/watch":
				id = u.Query().Get("v")
			case strings.HasPrefix(u.Path, "/shorts/"):
				id = strings.TrimPrefix(u.Path, "/shorts/")
			case strings.HasPrefix(u.Path, "/live/"):
				id = strings.TrimPrefix(u.Path, "/live/")
			}
		}
		if !videoID.MatchString(id) || u.Query().Has("list") {
			return "", errors.New("Use one YouTube video, not a playlist or another website.")
		}
		return "https://www.youtube.com/watch?v=" + id, nil
	}
	return "ytsearch1:" + q, nil
}

func mediaURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || !strings.HasSuffix(strings.ToLower(u.Host), ".googlevideo.com") {
		return "", errors.New("YouTube returned an unsupported audio URL.")
	}
	return u.String(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("tool output too large")
	}
	return b.Buffer.Write(p)
}

// process groups also reap yt-dlp's JS helpers on skip/stop, not just the parent.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 2 * time.Second
	c.Stderr = io.Discard
	return c
}

var ytArgs = []string{"--ignore-config", "--no-playlist", "--no-warnings", "--no-progress", "--no-cache-dir", "--socket-timeout", "10", "--retries", "1", "--extractor-retries", "1", "--js-runtimes", "deno"}

func ytOutput(ctx context.Context, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	c := command(ctx, "yt-dlp", append(append([]string{}, ytArgs...), args...)...)
	out := &limitedBuffer{limit: limit}
	c.Stdout = out
	if err := c.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("YouTube lookup failed: %w", err)
	}
	return out.Bytes(), nil
}

func Lookup(ctx context.Context, query string) (Track, error) {
	data, err := ytOutput(ctx, 256<<10, "--skip-download", "--dump-single-json", "--", query)
	if err != nil {
		return Track{}, err
	}
	return parseMetadata(data, query)
}

func parseMetadata(data []byte, query string) (Track, error) {
	type metadata struct {
		ID         string            `json:"id"`
		Title      string            `json:"title"`
		Duration   float64           `json:"duration"`
		Extractor  string            `json:"extractor_key"`
		IsLive     bool              `json:"is_live"`
		LiveStatus string            `json:"live_status"`
		Entries    []json.RawMessage `json:"entries"`
	}
	var m metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Track{}, errors.New("Invalid YouTube metadata.")
	}
	if strings.HasPrefix(query, "ytsearch1:") {
		if len(m.Entries) != 1 {
			return Track{}, errors.New("No YouTube video found.")
		}
		entry := m.Entries[0]
		m = metadata{}
		if json.Unmarshal(entry, &m) != nil {
			return Track{}, errors.New("Invalid YouTube search result.")
		}
	}
	if !videoID.MatchString(m.ID) || m.Extractor != "Youtube" || m.IsLive || m.LiveStatus == "is_live" || m.LiveStatus == "is_upcoming" || !(m.Duration > 0 && m.Duration <= MaxDuration.Seconds()) {
		return Track{}, errors.New("Only finite YouTube videos up to 3 hours are supported.")
	}
	title := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, m.Title)
	runes := []rune(title)
	if len(runes) > 100 {
		title = string(runes[:100])
	}
	if title == "" {
		title = m.ID
	}
	return Track{Query: query, ID: m.ID, Title: title, Duration: time.Duration(m.Duration * float64(time.Second))}, nil
}

// Run resolves expiring media URLs only when a track is about to play.
func Run(ctx context.Context, track Track, playback Playback, info func(Track), wait func(context.Context) error, sink Sink) error {
	if playback.Volume < 0 || playback.Volume > 100 || playback.Offset < 0 || playback.Offset > MaxDuration {
		return errors.New("Invalid playback offset or volume.")
	}
	if track.ID == "" {
		var err error
		track, err = Lookup(ctx, track.Query)
		if err != nil {
			return err
		}
	}
	if track.Duration <= 0 || track.Duration > MaxDuration || playback.Offset > track.Duration {
		return errors.New("Invalid track duration or playback offset.")
	}
	info(track)
	if playback.Offset == track.Duration {
		return nil
	}
	data, err := ytOutput(ctx, 8192, "--format", "bestaudio[protocol=https]", "--get-url", "--", "https://www.youtube.com/watch?v="+track.ID)
	if err != nil {
		return err
	}
	stream, err := mediaURL(string(data))
	if err != nil {
		return err
	}
	c := command(ctx, "ffmpeg", decoderArgs(stream, track.Duration, playback)...)
	return runDecoder(ctx, c, track.Duration-playback.Offset, wait, sink)
}

func decoderArgs(stream string, duration time.Duration, playback Playback) []string {
	return []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "https,tls,tcp", "-rw_timeout", "15000000", "-ss", fmt.Sprint(playback.Offset.Seconds()), "-i", stream, "-vn", "-sn", "-dn", "-af", fmt.Sprintf("volume=%f", float64(playback.Volume)/100), "-ac", "2", "-ar", "48000", "-c:a", "libopus", "-b:a", "96k", "-frame_duration", "20", "-application", "audio", "-t", fmt.Sprint((duration - playback.Offset).Seconds()), "-f", "ogg", "-page_duration", "20000", "pipe:1"}
}

func runDecoder(ctx context.Context, c *exec.Cmd, remaining time.Duration, wait func(context.Context) error, sink Sink) error {
	stdout, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err = c.Start(); err != nil {
		return fmt.Errorf("Starting ffmpeg: %w", err)
	}
	// Every exit kills and waits, including parser/write errors and paused cancellation.
	waited := false
	defer func() {
		if !waited {
			_ = c.Cancel()
			_ = stdout.Close()
			_ = c.Wait()
		}
	}()
	ogg := newOggReader(stdout)
	next := time.Now()
	// Ogg/Opus adds encoder padding; emit at most the remaining track's rounded-up frame count.
	for emitted := time.Duration(0); emitted < remaining; emitted += 20 * time.Millisecond {
		if err = wait(ctx); err != nil {
			return err
		}
		packet, readErr := ogg.Next()
		if readErr == io.EOF {
			err = c.Wait()
			waited = true
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return fmt.Errorf("ffmpeg failed: %w", err)
			}
			return nil
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("Invalid ffmpeg audio: %w", readErr)
		}
		if err = wait(ctx); err != nil {
			return err
		}
		if delay := time.Until(next); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if err = sink.WriteFrame(ctx, packet); err != nil {
			return err
		}
		next = next.Add(20 * time.Millisecond)
		if time.Since(next) > 40*time.Millisecond {
			next = time.Now().Add(20 * time.Millisecond)
		}
	}
	return nil
}
