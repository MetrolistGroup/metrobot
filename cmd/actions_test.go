package cmd

import (
	"strings"
	"testing"

	gh "github.com/MetrolistGroup/metrobot/github"
)

func TestNightlyFormattingUsesPublishedAssets(t *testing.T) {
	release := &gh.ReleaseInfo{HTMLURL: "https://github.com/MetrolistGroup/Metrolist/releases/tag/nightly", Assets: []gh.ReleaseAsset{
		{Name: "Metrolist.apk", DownloadURL: "https://example.com/Metrolist.apk"},
		{Name: "Metrolist-Desktop-windows.zip", DownloadURL: "https://example.com/windows.zip"},
		{Name: "metrolist-desktop-linux-x64-14.0.0+54715a5.AppImage", DownloadURL: "https://example.com/linux.AppImage"},
		{Name: "Metrolist-with-Google-Cast.apk", DownloadURL: "https://example.com/legacy.apk"},
	}}
	for _, telegram := range []bool{false, true} {
		got := formatNightlyRelease(release, telegram)
		for _, want := range []string{"Android 7+", "Windows x64", "Linux x64", "14.0.0+54715a5", "Unstable", "Back up", "over stable in place", release.HTMLURL} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in %s", want, got)
			}
		}
		for _, forbidden := range []string{"nightly.link", "legacy.apk", "GMS", "FOSS", "workflow"} {
			if strings.Contains(got, forbidden) {
				t.Errorf("obsolete download %q", forbidden)
			}
		}
	}
	if got := formatNightlyRelease(nil, false); !strings.Contains(got, "No published nightly") {
		t.Fatal(got)
	}
	release.Assets = nil
	if got := formatNightlyRelease(release, false); !strings.Contains(got, "No supported nightly assets") {
		t.Fatal(got)
	}
	release.Assets = []gh.ReleaseAsset{{Name: "metrolist-desktop-linux-x64-14.0.0+<sha>.AppImage", DownloadURL: "https://example.com/?a=1&b=2"}}
	got := formatNightlyRelease(release, true)
	if !strings.Contains(got, "&lt;sha&gt;") || !strings.Contains(got, "a=1&amp;b=2") {
		t.Fatal("unescaped Telegram HTML", got)
	}
}
