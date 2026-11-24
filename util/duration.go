package util

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Plural "s" on minutes needs the full "min" spelling so "1ms" is rejected
// instead of read as one minute.
const durationUnit = `(y(?:r|ear)?s?|mo(?:n(?:th)?)?s?|d(?:ay)?s?|h(?:r|our)?s?|m(?:in(?:ute)?s?)?|s(?:ec(?:ond)?)?s?)`

var (
	durationPattern     = regexp.MustCompile(`(?i)(\d+)\s*` + durationUnit)
	fullDurationPattern = regexp.MustCompile(`(?i)^(?:\d+\s*` + durationUnit + `\s*)+$`)
)

// ParseDuration parses a human-readable duration string into a time.Duration.
// Supported units: y/yr/year, mo/mon/month, d/day, h/hr/hour, m/min/minute, s/sec/second.
// Supports combinations like "1h2m", "4h36m", "1y2mon3d". The whole input must
// be positive whole-number parts; anything else, or a total that overflows,
// is an error.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	if !fullDurationPattern.MatchString(s) {
		return 0, fmt.Errorf("unrecognised duration format: %q", s)
	}

	var total time.Duration
	for _, match := range durationPattern.FindAllStringSubmatch(s, -1) {
		value, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("duration %q is too long", s)
		}

		unit := strings.ToLower(match[2])
		var size time.Duration
		switch {
		case strings.HasPrefix(unit, "y"):
			size = 365 * 24 * time.Hour
		case strings.HasPrefix(unit, "mo"):
			size = 30 * 24 * time.Hour
		case strings.HasPrefix(unit, "d"):
			size = 24 * time.Hour
		case strings.HasPrefix(unit, "h"):
			size = time.Hour
		case strings.HasPrefix(unit, "m"):
			size = time.Minute
		case strings.HasPrefix(unit, "s"):
			size = time.Second
		default:
			return 0, fmt.Errorf("unrecognised unit %q in duration", unit)
		}
		if value > int64(math.MaxInt64-total)/int64(size) {
			return 0, fmt.Errorf("duration %q is too long", s)
		}
		total += time.Duration(value) * size
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
