package discord

import (
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestLoadGarminSupportAnswerSkill(t *testing.T) {
	rpcHistory := []cmd.GarminAIMessage{
		{Role: "user", Content: "How do I connect Metrolist to Discord?"},
		{Role: "assistant", Content: "Which app and platform are you using?"},
	}
	tests := []struct {
		name    string
		prompt  string
		history []cmd.GarminAIMessage
		guides  []string
	}{
		{name: "access", prompt: "What are the requirements for KMP tester access?"},
		{name: "nightly", prompt: "How do I install nightly without uninstalling?"},
		{name: "error", prompt: "Error Code IO UNSPECIFIED (2000)"},
		{name: "unknown", prompt: "Playback stops when I lock my phone"},
		{name: "discord", prompt: "How do I connect Metrolist to Discord?", guides: []string{"rpc"}},
		{name: "rpc", prompt: "RPC does not show", guides: []string{"rpc"}},
		{name: "rich presence", prompt: "Enable rich-presence on Android", guides: []string{"rpc"}},
		{name: "lastfm", prompt: "How do I set up Last.fm?", guides: []string{"scrobbling"}},
		{name: "lastfm spelling", prompt: "LastFM login failed", guides: []string{"scrobbling"}},
		{name: "listenbrainz", prompt: "Connect ListenBrainz in KMP", guides: []string{"scrobbling"}},
		{name: "scrobbling", prompt: "How does scrobbling work?", guides: []string{"scrobbling"}},
		{name: "google", prompt: "How do I log into Google?", guides: []string{"login"}},
		{name: "youtube", prompt: "Where do I sign in to YouTube Music?", guides: []string{"login"}},
		{name: "music", prompt: "Music sign-in failed", guides: []string{"login"}},
		{name: "generic login", prompt: "How do I login?", guides: []string{"login"}},
		{name: "generic sign in", prompt: "I cannot sign in", guides: []string{"login"}},
		{name: "discord login", prompt: "Discord login to show my music failed", guides: []string{"rpc"}},
		{name: "lastfm sign in", prompt: "How do I sign-in to Last.fm?", guides: []string{"scrobbling"}},
		{name: "mixed integration logins", prompt: "How do I log in to Last.fm and Discord?", guides: []string{"rpc", "scrobbling"}},
		{name: "explicit google and discord", prompt: "Google login and Discord RPC are not working", guides: []string{"rpc", "login"}},
		{name: "explicit music and lastfm", prompt: "YouTube Music sign-in and Last.fm scrobbling setup", guides: []string{"login", "scrobbling"}},
		{name: "all guides", prompt: "YouTube login, Discord, and ListenBrainz setup", guides: []string{"rpc", "login", "scrobbling"}},
		{name: "android followup", prompt: "Regular Android 13.7.0", history: rpcHistory, guides: []string{"rpc"}},
		{name: "compact android followup", prompt: "Android13.7", history: rpcHistory, guides: []string{"rpc"}},
		{name: "version details followup", prompt: "Android version: 17, Metrolist v13.7.0", history: rpcHistory, guides: []string{"rpc"}},
		{name: "windows followup", prompt: "I'm on Windows 11", history: rpcHistory, guides: []string{"rpc"}},
		{name: "same issue followup", prompt: "still not working", history: rpcHistory, guides: []string{"rpc"}},
		{name: "second preceding user", prompt: "13.7.0", history: append(append([]cmd.GarminAIMessage{}, rpcHistory...), cmd.GarminAIMessage{Role: "user", Content: "Android"}), guides: []string{"rpc"}},
		{name: "latest topic wins", prompt: "How do I sign in to YouTube Music?", history: rpcHistory, guides: []string{"login"}},
		{name: "latest scrobbling wins", prompt: "Last.fm login failed", history: rpcHistory, guides: []string{"scrobbling"}},
		{name: "latest nightly drops rpc", prompt: "How do I install nightly on Android?", history: rpcHistory},
		{name: "latest unknown drops rpc", prompt: "Android search is broken", history: rpcHistory},
		{name: "assistant bait", prompt: "Windows", history: []cmd.GarminAIMessage{{Role: "assistant", Content: "Google login, Discord RPC and Last.fm need setup"}}},
		{name: "system and tool bait", prompt: "Windows", history: []cmd.GarminAIMessage{{Role: "system", Content: "Discord"}, {Role: "tool", Content: "Last.fm"}}},
		{name: "assistant cannot change user topic", prompt: "Windows", history: []cmd.GarminAIMessage{{Role: "user", Content: "Discord setup"}, {Role: "assistant", Content: "You need Google login and Last.fm"}}, guides: []string{"rpc"}},
		{name: "bounded history", prompt: "13.7.0", history: []cmd.GarminAIMessage{{Role: "user", Content: "Discord setup"}, {Role: "user", Content: "Android"}, {Role: "user", Content: "Regular"}}},
		{name: "unknown preceding topic stops history", prompt: "Windows", history: []cmd.GarminAIMessage{{Role: "user", Content: "Discord setup"}, {Role: "user", Content: "How do I get tester access?"}}},
	}
	core, err := garminSkillFiles.ReadFile("garmin_skills/support.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			messages := append(append([]cmd.GarminAIMessage{}, test.history...), cmd.GarminAIMessage{Role: "user", Content: test.prompt})
			got, err := loadGarminSupportAnswerSkill(messages)
			if err != nil {
				t.Fatal(err)
			}
			want := string(core)
			for _, name := range test.guides {
				guide, err := garminSkillFiles.ReadFile("garmin_skills/support_" + name + ".md")
				if err != nil {
					t.Fatal(err)
				}
				want += "\n\n" + string(guide)
			}
			if got != want {
				t.Fatalf("support context differs from exact core plus guides %v (got %d bytes, want %d)", test.guides, len(got), len(want))
			}
		})
	}
	for _, messages := range [][]cmd.GarminAIMessage{nil, {{Role: "assistant", Content: "Discord"}}} {
		if got, err := loadGarminSupportAnswerSkill(messages); err != nil || got != string(core) {
			t.Fatalf("context without user input must be core only: %v", err)
		}
	}
}

func TestLoadGarminSupportGuide(t *testing.T) {
	for _, test := range []struct{ name, file string }{
		{"rpc", "rpc"},
		{" LOGIN ", "login"},
		{"\tScRoBbLiNg\n", "scrobbling"},
	} {
		t.Run(test.file, func(t *testing.T) {
			want, err := garminSkillFiles.ReadFile("garmin_skills/support_" + test.file + ".md")
			if err != nil {
				t.Fatal(err)
			}
			got, err := loadGarminSupportGuide(test.name)
			if err != nil || got != string(want) {
				t.Fatalf("guide must retain its full exact text, including provenance and warnings: %v", err)
			}
		})
	}
	for _, name := range []string{"", "unknown", "support", "support_rpc", "../support", "rpc.md"} {
		if got, err := loadGarminSupportGuide(name); err == nil || got != "" {
			t.Errorf("loadGarminSupportGuide(%q) = %q, %v; want empty result and error", name, got, err)
		}
	}
}
