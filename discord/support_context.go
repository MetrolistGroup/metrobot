package discord

import (
	"fmt"
	"strings"
)

func loadGarminSupportAnswerSkill() (string, error) {
	data, err := garminSkillFiles.ReadFile("garmin_skills/support.md")
	if err != nil {
		return "", fmt.Errorf("loading support skill: %w", err)
	}
	return string(data), nil
}

func loadGarminSupportGuide(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "rpc", "login", "scrobbling", "playlists":
		data, err := garminSkillFiles.ReadFile("garmin_skills/support_" + name + ".md")
		if err != nil {
			return "", fmt.Errorf("loading support guide %s: %w", name, err)
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("unknown support guide %q; available guides: rpc, login, scrobbling, playlists", name)
	}
}
