package discord

import (
	"fmt"
	"strings"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func loadGarminSupportAnswerSkill(messages []cmd.GarminAIMessage) (string, error) {
	data, err := garminSkillFiles.ReadFile("garmin_skills/support.md")
	if err != nil {
		return "", fmt.Errorf("loading support skill: %w", err)
	}

	// shortcut: English topic hints only; load_support_guide covers novel or non-English wording.
	var selected [4]bool
	userTurns := 0
	for i := len(messages) - 1; i >= 0 && userTurns < 3; i-- {
		if messages[i].Role != "user" {
			continue
		}
		userTurns++
		prompt := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(messages[i].Content)), "-", " ")
		rpc := containsAnyGarminPhrase(prompt, "discord", "rich presence", "rpc")
		scrobbling := containsAnyGarminPhrase(prompt, "last.fm", "lastfm", "listenbrainz", "scrobbl")
		login := containsAnyGarminPhrase(prompt, "login", "log in", "signin", "sign in", "signing in")
		musicLogin := containsAnyGarminPhrase(prompt, "google", "youtube", "music login", "music sign in", "log in to music", "sign in to music")
		playlists := strings.Contains(prompt, "playlist")
		selected = [4]bool{rpc, login && (musicLogin || (!rpc && !scrobbling)), scrobbling, playlists}
		if selected[0] || selected[1] || selected[2] || selected[3] {
			break
		}

		// A terse follow-up can inherit a user topic; a fresh unknown request cannot.
		switch prompt {
		case "still not working", "still broken", "same issue", "same problem", "that didn't help":
			continue
		}
		followup := prompt != ""
		for _, word := range strings.Fields(prompt) {
			switch strings.Trim(word, "0123456789.,!?:") {
			case "", "v", "android", "windows", "linux", "macos", "ios", "desktop", "kmp", "regular", "standalone", "metrolist", "version", "on", "using", "i'm":
			default:
				followup = false
			}
		}
		if !followup {
			break
		}
	}

	content := string(data)
	for i, name := range []string{"rpc", "login", "scrobbling", "playlists"} {
		if !selected[i] {
			continue
		}
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
