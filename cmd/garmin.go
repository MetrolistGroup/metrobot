package cmd

import "strings"

// GarminProcessor handles "Ok Garmin" voice assistant trigger processing
type GarminProcessor struct {
	supportedCommands map[string]bool
}

// NewGarminProcessor creates a new Garmin trigger processor
func NewGarminProcessor() *GarminProcessor {
	return &GarminProcessor{
		supportedCommands: map[string]bool{
			"ban":  true,
			"dban": true,
			"tban": true,
			"sban": true,
			"mute": true,
			"warn": true,
			"notes": true,
			"note": true,
		},
	}
}

// ProcessTrigger checks if a message starts with "Ok Garmin" and converts it to command format
func (gp *GarminProcessor) ProcessTrigger(content string) string {
	// Case insensitive check for "ok garmin" at the start
	lower := strings.ToLower(content)
	if !strings.HasPrefix(lower, "ok garmin") {
		return content
	}

	// Find where "ok garmin" ends
	garminLen := len("ok garmin")
	if len(content) <= garminLen {
		return content
	}

	// Skip past "ok garmin"
	remainder := content[garminLen:]

	// Skip optional comma and whitespace
	remainder = strings.TrimLeftFunc(remainder, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})

	if remainder == "" {
		return content
	}

	// Convert to command format: add ! prefix if it's a supported command
	words := strings.Fields(remainder)
	if len(words) > 0 {
		command := strings.ToLower(words[0])
		if gp.supportedCommands[command] {
			return "!" + remainder
		}
	}

	return content
}
