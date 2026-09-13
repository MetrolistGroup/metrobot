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
