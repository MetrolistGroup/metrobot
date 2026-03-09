package util

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var durationPattern = regexp.MustCompile(`(?i)(\d+)\s*(y(?:r|ear)?s?|mon(?:th)?s?|d(?:ay)?s?|h(?:r|our)?s?|m(?:in(?:ute)?)?s?|s(?:ec(?:ond)?)?s?)`)

// ParseDuration parses a human-readable duration string into a time.Duration.
// Supported units: y/yr/year, mon/month, d/day, h/hr/hour, m/min/minute, s/sec/second.
// Supports combinations like "1h2m", "4h36m", "1y2mon3d".
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	matches := durationPattern.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return 0, fmt.Errorf("unrecognised duration format: %q", s)
	}

	var total time.Duration

	for _, match := range matches {
		value, err := strconv.Atoi(match[1])
		if err != nil {
			return 0, fmt.Errorf("invalid number %q in duration", match[1])
		}

		unit := strings.ToLower(match[2])

		switch {
		case strings.HasPrefix(unit, "y"):
			total += time.Duration(value) * 365 * 24 * time.Hour
		case strings.HasPrefix(unit, "mon"):
			total += time.Duration(value) * 30 * 24 * time.Hour
		case strings.HasPrefix(unit, "d"):
			total += time.Duration(value) * 24 * time.Hour
		case strings.HasPrefix(unit, "h"):
			total += time.Duration(value) * time.Hour
		case strings.HasPrefix(unit, "m"):
			total += time.Duration(value) * time.Minute
		case strings.HasPrefix(unit, "s"):
			total += time.Duration(value) * time.Second
		default:
			return 0, fmt.Errorf("unrecognised unit %q in duration", unit)
		}
	}

	if total == 0 {
		return 0, fmt.Errorf("duration is zero")
	}

	return total, nil
}

// FormatDuration returns a human-readable string for a time.Duration.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}

	var parts []string

	days := int(d.Hours()) / 24
	if days > 0 {
		if days >= 365 {
			years := days / 365
			days %= 365
			parts = append(parts, fmt.Sprintf("%dy", years))
		}
		if days >= 30 {
			months := days / 30
			days %= 30
			parts = append(parts, fmt.Sprintf("%dmon", months))
		}
		if days > 0 {
			parts = append(parts, fmt.Sprintf("%dd", days))
		}
	}

	hours := int(d.Hours()) % 24
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}

	minutes := int(d.Minutes()) % 60
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}

	seconds := int(d.Seconds()) % 60
	if seconds > 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}

	if len(parts) == 0 {
		return "0s"
	}

	return strings.Join(parts, "")
}
