package cmd

import (
	"strings"
	"testing"

	gh "github.com/MetrolistGroup/metrobot/github"
)

func TestSelectReleaseAssetsPrefersOfficialAPKs(t *testing.T) {
	assets := []gh.ReleaseAsset{
		{Name: "random.apk"},
		{Name: "Metrolist-with-Google-Cast.apk"},
		{Name: "Metrolist.apk"},
		{Name: "docs.txt"},
	}

	selected := selectReleaseAssets(assets)
	if len(selected) != 2 {
		t.Fatalf("selectReleaseAssets() len = %d, want 2", len(selected))
	}
	if selected[0].Name != "Metrolist.apk" || selected[1].Name != "Metrolist-with-Google-Cast.apk" {
		t.Fatalf("selectReleaseAssets() = %#v", selected)
	}
}

func TestFormatReleaseShowsDownloadTotals(t *testing.T) {
	rel := &gh.ReleaseInfo{
		TagName: "v1.0.0",
		Assets: []gh.ReleaseAsset{
			{Name: "Metrolist.apk", DownloadURL: "https://example.com/a", Size: 2048, DownloadCount: 10},
			{Name: "Metrolist-with-Google-Cast.apk", DownloadURL: "https://example.com/b", Size: 4096, DownloadCount: 12},
			{Name: "ignored.apk", DownloadURL: "https://example.com/c", Size: 1, DownloadCount: 99},
		},
	}

	formatted := formatRelease(rel, false)
	if !strings.Contains(formatted, "**Downloads:** 22 total") {
		t.Fatalf("formatted release missing total downloads: %q", formatted)
	}
	if strings.Contains(formatted, "ignored.apk") {
		t.Fatalf("formatted release should filter ignored apk: %q", formatted)
	}
}

func TestFormatDiscordReleaseBodyConvertsGitHubCallouts(t *testing.T) {
	body := "> [!WARNING]\n> Listen Together doesn't work in v13.2.1! Use v13.2.0 if you need it."
	formatted := formatDiscordReleaseBody(body)
	want := "**Warning:** Listen Together doesn't work in v13.2.1! Use v13.2.0 if you need it."
	if formatted != want {
		t.Fatalf("formatDiscordReleaseBody() = %q, want %q", formatted, want)
	}
}

func TestFormatDiscordReleaseBodyConvertsHeadingsAndBullets(t *testing.T) {
	body := "## Hot Fixes\n- Fix interface lag issue\n- Fix navigate local playlists pinned in speed dial"
	formatted := formatDiscordReleaseBody(body)

	if !strings.Contains(formatted, "**Hot Fixes**") {
		t.Fatalf("formatted body missing heading: %q", formatted)
	}
	if !strings.Contains(formatted, "• Fix interface lag issue") {
		t.Fatalf("formatted body missing bullet conversion: %q", formatted)
	}
	if !strings.Contains(formatted, "• Fix navigate local playlists pinned in speed dial") {
		t.Fatalf("formatted body missing second bullet conversion: %q", formatted)
	}
}
