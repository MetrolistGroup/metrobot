package music

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizeQuery(t *testing.T) {
	for input, want := range map[string]string{
		"  artist song  ":                                           "ytsearch1:artist song",
		"-o /tmp/no; echo hi":                                       "ytsearch1:-o /tmp/no; echo hi",
		"https://youtu.be/abcdefghijk?t=10":                         "https://www.youtube.com/watch?v=abcdefghijk",
		"https://music.youtube.com/watch?v=abcdefghijk&si=tracking": "https://www.youtube.com/watch?v=abcdefghijk",
		"https://www.youtube.com/shorts/abcdefghijk":                "https://www.youtube.com/watch?v=abcdefghijk",
	} {
		got, err := NormalizeQuery(input)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "\n", strings.Repeat("a", MaxQuery+1), "line\nbreak", "https://127.0.0.1/watch?v=abcdefghijk", "http://youtu.be/abcdefghijk", "https://youtube.com.evil/watch?v=abcdefghijk", "https://user@youtube.com/watch?v=abcdefghijk", "https://youtube.com:443/watch?v=abcdefghijk", "https://youtube.com/watch?v=abc", "https://youtube.com/playlist?list=abc", "https://youtu.be/abcdefghijk?list=abc", "https://youtu.be/abcdefghijk/extra", "file:///etc/passwd", "//localhost/x", "www.youtube.com/watch?v=abcdefghijk", "a\x00b"} {
		if q, err := NormalizeQuery(input); err == nil {
			t.Errorf("accepted %q as %q", input, q)
		}
	}
}

func TestMetadataBoundary(t *testing.T) {
	valid := `{"id":"abcdefghijk","title":"track","duration":120,"extractor_key":"Youtube"}`
	for _, fixture := range []struct{ data, query string }{{valid, "https://www.youtube.com/watch?v=abcdefghijk"}, {`{"entries":[` + valid + `]}`, "ytsearch1:track"}} {
		track, err := parseMetadata([]byte(fixture.data), fixture.query)
		if err != nil || track.ID != "abcdefghijk" || track.Duration != 2*time.Minute {
			t.Fatalf("%+v %v", track, err)
		}
	}
	for _, data := range []string{
		`{}`, `{"entries":[]}`, `{"entries":[null]}`,
		strings.Replace(valid, `"Youtube"`, `"Generic"`, 1),
		strings.Replace(valid, `120`, `0`, 1), strings.Replace(valid, `120`, `10801`, 1),
		strings.Replace(valid, `"track"`, `"track","is_live":true`, 1),
		strings.Replace(valid, `"track"`, `"track","live_status":"is_upcoming"`, 1),
		strings.Replace(valid, `"abcdefghijk"`, `"../../x"`, 1),
	} {
		if track, err := parseMetadata([]byte(data), "direct"); err == nil {
			t.Errorf("accepted %s: %+v", data, track)
		}
	}
}

func TestMediaURLBoundary(t *testing.T) {
	if _, err := mediaURL("https://rr1---sn-a.googlevideo.com/videoplayback?sig=abc\n"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://a.googlevideo.com/x", "https://googlevideo.com/x", "https://googlevideo.com.evil/x", "https://googlevideo.com@127.0.0.1/x", "https://a.googlevideo.com:443/x", "https://127.0.0.1/x", "https://youtube.com/x", "https://a.googlevideo.com/x\nhttps://b.googlevideo.com/x"} {
		if _, err := mediaURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	b := &limitedBuffer{limit: 4}
	if _, err := b.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("5")); err == nil || b.Len() != 4 {
		t.Fatal("output cap did not hold")
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("METROBOT_MUSIC_HELPER") != "1" {
		return
	}
	if os.Args[len(os.Args)-1] == "block" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	_, _ = io.WriteString(os.Stdout, os.Args[len(os.Args)-1])
	os.Exit(0)
}

func TestCommandLiteralArgumentsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	literal := `; touch /tmp/not-executed $(whoami)`
	cmd := command(ctx, os.Args[0], "-test.run=^TestProcessHelper$", "--", literal)
	cmd.Env = append(os.Environ(), "METROBOT_MUSIC_HELPER=1")
	got, err := cmd.Output()
	if err != nil || string(got) != literal {
		t.Fatalf("argument was not literal: %q %v", got, err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	cmd = command(ctx, os.Args[0], "-test.run=^TestProcessHelper$", "--", "block")
	cmd.Env = append(os.Environ(), "METROBOT_MUSIC_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil || cmd.ProcessState == nil {
			t.Fatal("canceled process not killed and reaped")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled process did not exit")
	}
}
