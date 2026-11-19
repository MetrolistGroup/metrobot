package discord

import (
	"testing"
	"time"
)

func TestParseReminderDelay(t *testing.T) {
	for spec, want := range map[string]time.Duration{
		"45s":   45 * time.Second,
		"10m":   10 * time.Minute,
		"1h30m": 90 * time.Minute,
		"2d":    48 * time.Hour,
		"1w":    7 * 24 * time.Hour,
		"10M":   10 * time.Minute, // units are case-insensitive
	} {
		if got, ok := parseReminderDelay(spec); !ok || got != want {
			t.Errorf("%q = %v (ok=%v), want %v", spec, got, ok, want)
		}
	}
	for _, bad := range []string{"", "10", "m", "1x", "1h30", "1h 30m", "999999d", "1.5h"} {
		if _, ok := parseReminderDelay(bad); ok {
			t.Errorf("%q accepted, want rejected", bad)
		}
	}
	// "0m" is valid syntax for a zero delay; the handler's range check rejects it.
	if got, ok := parseReminderDelay("0m"); !ok || got != 0 {
		t.Errorf("0m = %v (ok=%v), want 0", got, ok)
	}
}

func TestParseReminderArgs(t *testing.T) {
	if spec, text := parseReminderArgs("1h 30m call mom"); spec != "1h30m" || text != "call mom" {
		t.Errorf("split = %q %q, want 1h30m call mom", spec, text)
	}
	if spec, text := parseReminderArgs("45m pizza out"); spec != "45m" || text != "pizza out" {
		t.Errorf("single spec = %q %q, want 45m pizza out", spec, text)
	}
	if spec, text := parseReminderArgs("1h 30m"); spec != "1h30m" || text != "" {
		t.Errorf("no text = %q %q, want 1h30m and empty text", spec, text)
	}
	if spec, text := parseReminderArgs("10m 5 hello"); spec != "10m" || text != "5 hello" {
		t.Errorf("non-duration second token = %q %q, want 10m and 5 hello", spec, text)
	}
}

func TestReminderContent(t *testing.T) {
	if got := reminderContent("123", "pizza out"); got != "⏰ <@123>, your reminder: pizza out" {
		t.Errorf("content = %q", got)
	}
	if got := reminderContent("123", ""); got != "⏰ <@123>, this is your reminder." {
		t.Errorf("empty content = %q", got)
	}
}
