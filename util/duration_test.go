package util

import (
	"testing"
	"time"
)

func TestParseDurationMonthAbbreviation(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"6mo": 6 * 30 * 24 * time.Hour,
		"6m":  6 * time.Minute,
	} {
		got, err := ParseDuration(input)
		if err != nil || got != want {
			t.Fatalf("ParseDuration(%q) = %s, %v; want %s, nil", input, got, err, want)
		}
	}
}

func TestParseDurationRejectsPartialAndOverflowingInput(t *testing.T) {
	for _, input := range []string{"1.5h", "-1h", "1ms", "300y", "600y", "h", "1h junk", "1h-2m", "99999999999999999999s"} {
		if got, err := ParseDuration(input); err == nil {
			t.Errorf("ParseDuration(%q) = %s, want error", input, got)
		}
	}
	for input, want := range map[string]time.Duration{
		"1h30m":       90 * time.Minute,
		"1y2mon3d":    (365 + 60 + 3) * 24 * time.Hour,
		"2 hours 5 m": 2*time.Hour + 5*time.Minute,
		"10mins":      10 * time.Minute,
		"3mos":        90 * 24 * time.Hour,
		"45s":         45 * time.Second,
		"292y":        292 * 365 * 24 * time.Hour,
	} {
		got, err := ParseDuration(input)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %s, %v; want %s", input, got, err, want)
		}
	}
}
