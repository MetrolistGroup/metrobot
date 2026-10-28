package discord

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type supportLiveSource struct {
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
}

// The paid regression must prove build inclusion, not just count tool calls.
func verifyNightlyReplayEvidence(sources []supportLiveSource, body, fix string) error {
	if fix == "" {
		for _, source := range sources {
			var args struct {
				Ref string `json:"ref"`
			}
			if source.Tool == "get_metrolist_commit" && json.Unmarshal([]byte(source.Arguments), &args) == nil && len(args.Ref) == 40 {
				if err := verifyNightlyReplayEvidence(sources, body, args.Ref); err == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("nightly recommendation has no inspected included patch")
	}
	for _, source := range sources {
		if source.Tool != "get_metrolist_changes" {
			continue
		}
		var args struct {
			Ref string `json:"ref"`
		}
		var comparison struct {
			Ref     string `json:"target_ref"`
			SHA     string `json:"target_sha"`
			Nightly struct {
				Tag    string `json:"tag_name"`
				SHA    string `json:"target_commitish"`
				URL    string `json:"html_url"`
				Assets []struct {
					Name  string `json:"name"`
					State string `json:"state"`
					URL   string `json:"browser_download_url"`
				} `json:"assets"`
			} `json:"nightly_release"`
			Commits []struct {
				SHA string `json:"sha"`
			} `json:"commits"`
		}
		if json.Unmarshal([]byte(source.Arguments), &args) != nil || json.Unmarshal([]byte(source.Result), &comparison) != nil {
			continue
		}
		if args.Ref != "nightly" || comparison.Ref != "nightly" || len(comparison.SHA) != 40 || comparison.SHA != comparison.Nightly.SHA || comparison.Nightly.Tag != "nightly" {
			continue
		}
		included, available, linked := false, false, false
		for _, commit := range comparison.Commits {
			included = included || commit.SHA == fix
		}
		for _, asset := range comparison.Nightly.Assets {
			if asset.State == "uploaded" && strings.HasSuffix(asset.Name, ".apk") && asset.URL != "" {
				available = true
				linked = linked || strings.Contains(body, asset.URL)
			}
		}
		linked = linked || comparison.Nightly.URL != "" && strings.Contains(body, comparison.Nightly.URL)
		if !included || !available || !linked {
			continue
		}
		for _, detail := range sources {
			if detail.Tool != "get_metrolist_commit" {
				continue
			}
			var requested struct {
				Ref string `json:"ref"`
			}
			var result struct {
				Commit struct {
					SHA   string `json:"sha"`
					Files []struct {
						Patch string `json:"patch"`
					} `json:"files"`
				} `json:"commit"`
			}
			if json.Unmarshal([]byte(detail.Arguments), &requested) != nil || json.Unmarshal([]byte(detail.Result), &result) != nil {
				continue
			}
			if requested.Ref == fix && result.Commit.SHA == fix {
				for _, file := range result.Commit.Files {
					if file.Patch != "" {
						return nil
					}
				}
			}
		}
	}
	return fmt.Errorf("reply lacks a matching published-nightly comparison, uploaded APK/link or inspected fix patch")
}

func TestReleaseLookupsKeepPrivateTextOutOfPublicArguments(t *testing.T) {
	sha := strings.Repeat("b", 40)
	if garminPublicQueryToolName("get_metrolist_changes") || !garminPublicQueryToolName("get_metrolist_commit") {
		t.Fatal("local history filtering or public commit lookup has wrong privacy scope")
	}
	args := garminToolArgs{Ref: sha}
	if garminPublicLookupArgumentsAllowed("get_metrolist_commit", args, []string{"downloads are slow"}) {
		t.Fatal("model invented a commit ref without public evidence")
	}
	if !garminPublicLookupArgumentsAllowed("get_metrolist_commit", args, []string{`{"sha":"` + sha + `"}`}) {
		t.Fatal("verified public commit could not be inspected")
	}
}

func TestNightlyReplayRejectsBrokenProvenance(t *testing.T) {
	head, fix := strings.Repeat("a", 40), strings.Repeat("b", 40)
	link := "https://github.com/owner/repo/releases/download/nightly/app.apk"
	for _, scenario := range []string{"valid", "main-only", "unrelated-commit", "wrong-build-link", "no-apk"} {
		t.Run(scenario, func(t *testing.T) {
			ref, inspected, body, state := "nightly", fix, link, "uploaded"
			if scenario == "main-only" {
				ref = "main"
			}
			if scenario == "unrelated-commit" {
				inspected = strings.Repeat("c", 40)
			}
			if scenario == "wrong-build-link" {
				body = "https://github.com/other/repo/releases/nightly"
			}
			if scenario == "no-apk" {
				state = "new"
			}
			sources := []supportLiveSource{
				{"get_metrolist_changes", fmt.Sprintf(`{"ref":%q}`, ref), fmt.Sprintf(`{"target_ref":%q,"target_sha":%q,"nightly_release":{"tag_name":"nightly","target_commitish":%q,"assets":[{"name":"app.apk","state":%q,"browser_download_url":%q}]},"commits":[{"sha":%q}]}`, ref, head, head, state, link, fix)},
				{"get_metrolist_commit", fmt.Sprintf(`{"ref":%q}`, inspected), fmt.Sprintf(`{"commit":{"sha":%q,"files":[{"patch":"+ range=0"}]}}`, inspected)},
			}
			err := verifyNightlyReplayEvidence(sources, body, fix)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("provenance %s: %v", scenario, err)
			}
		})
	}
}
