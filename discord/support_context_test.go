package discord

import "testing"

func TestLoadGarminSupportAnswerSkillIsCoreOnly(t *testing.T) {
	core, err := garminSkillFiles.ReadFile("garmin_skills/support.md")
	if err != nil {
		t.Fatal(err)
	}
	project, err := loadGarminSkill("metrolist")
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadGarminSupportAnswerSkill()
	if err != nil || got != string(core)+"\n\nVerified project reference:\n"+project {
		t.Fatalf("dedicated support context must contain only the core/project facts without setup preloads: %v", err)
	}
}

func TestLoadGarminSupportGuide(t *testing.T) {
	for _, test := range []struct{ name, file string }{
		{"rpc", "rpc"},
		{"playlists", "playlists"},
		{"discovery", "discovery"},
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
