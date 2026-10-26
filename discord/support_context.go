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
	project, err := loadGarminSkill("metrolist")
	if err != nil {
		return "", err
	}
	return string(data) + "\n\nVerified project reference:\n" + project, nil
}

// Keep the independent checker fully informed without preloading the answering agent.
func loadGarminSupportEvidenceSkill() (string, error) {
	content, err := loadGarminSupportAnswerSkill()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"rpc", "login", "scrobbling", "playlists"} {
		guide, err := loadGarminSupportGuide(name)
		if err != nil {
			return "", err
		}
		content += "\n\n" + guide
	}
	return content, nil
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
