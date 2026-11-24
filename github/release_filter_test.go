package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v93/github"
)

func releaseListClient(t *testing.T, listing string) *ReleasesClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/repo" {
			fmt.Fprint(w, `{"private":false}`)
			return
		}
		if r.URL.Path == "/repos/owner/repo/releases/latest" {
			var releases []json.RawMessage
			if err := json.Unmarshal([]byte(listing), &releases); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			for _, raw := range releases {
				var release struct {
					Draft, Prerelease bool
				}
				if err := json.Unmarshal(raw, &release); err != nil {
					t.Error(err)
					return
				}
				if !release.Draft && !release.Prerelease {
					_, _ = w.Write(raw)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, listing)
	}))
	t.Cleanup(server.Close)
	base := server.URL + "/"
	api, err := gh.NewClient(gh.WithHTTPClient(server.Client()), gh.WithURLs(&base, nil))
	if err != nil {
		t.Fatal(err)
	}
	return &ReleasesClient{client: api, owner: "owner", repo: "repo"}
}

func TestLatestReleaseUsesAuthoritativeEndpoint(t *testing.T) {
	var latestCalls, listingCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo":
			fmt.Fprint(w, `{"private":false}`)
		case "/repos/owner/repo/releases/latest":
			latestCalls++
			fmt.Fprint(w, `{"tag_name":"v14","assets":[{"name":"release.apk"}]}`)
		case "/repos/owner/repo/releases":
			listingCalls++
			fmt.Fprint(w, `[{"tag_name":"nightly","prerelease":true}]`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	base := server.URL + "/"
	api, err := gh.NewClient(gh.WithHTTPClient(server.Client()), gh.WithURLs(&base, nil))
	if err != nil {
		t.Fatal(err)
	}
	c := &ReleasesClient{client: api, owner: "owner", repo: "repo"}
	for range 2 {
		release, err := c.GetRelease(context.Background(), "latest")
		if err != nil || release == nil || release.TagName != "v14" || release.Assets[0].Name != "release.apk" {
			t.Fatalf("latest = %+v, %v", release, err)
		}
		release.Assets[0].Name = "caller mutation"
	}
	if latestCalls != 1 || listingCalls != 0 {
		t.Fatalf("latest calls=%d listing calls=%d", latestCalls, listingCalls)
	}
}

func TestReleasesHideDrafts(t *testing.T) {
	c := releaseListClient(t, `[{"tag_name":"v15-secret","draft":true,"body":"unpublished notes"},{"tag_name":"v14","draft":false}]`)
	for _, tag := range []string{"latest", "v15-secret"} {
		rel, err := c.GetRelease(context.Background(), tag)
		if err != nil {
			t.Fatal(err)
		}
		if rel != nil && rel.TagName == "v15-secret" {
			t.Fatalf("GetRelease(%q) exposed the draft", tag)
		}
	}
	releases, err := c.FetchReleases(context.Background())
	if err != nil || len(releases) != 1 || releases[0].TagName != "v14" {
		t.Fatalf("FetchReleases = %+v, %v", releases, err)
	}
}

func TestLatestReleaseSkipsPrereleases(t *testing.T) {
	c := releaseListClient(t, `[{"tag_name":"nightly","prerelease":true},{"tag_name":"v14.1-beta","prerelease":true},{"tag_name":"v14","prerelease":false}]`)
	rel, err := c.GetRelease(context.Background(), "latest")
	if err != nil || rel == nil || rel.TagName != "v14" {
		t.Fatalf("latest = %+v, %v; want v14", rel, err)
	}
	rel, err = c.GetRelease(context.Background(), "v14.1-beta")
	if err != nil || rel == nil || !rel.Prerelease {
		t.Fatalf("explicit prerelease lookup = %+v, %v", rel, err)
	}

	onlyNightly := releaseListClient(t, `[{"tag_name":"nightly","prerelease":true}]`)
	if rel, err := onlyNightly.GetRelease(context.Background(), "latest"); err != nil || rel != nil {
		t.Fatalf("latest with no stable release = %+v, %v; want none", rel, err)
	}
}
