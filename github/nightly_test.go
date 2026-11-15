package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gh "github.com/google/go-github/v93/github"
)

func TestNightlyResolvesTagCachesAndPreservesPublicBoundary(t *testing.T) {
	for _, mode := range []string{"public", "private", "missing", "draft", "stable", "wrong-tag"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/owner/repo":
					fmt.Fprintf(w, `{"private":%t}`, mode == "private")
				case "/repos/owner/repo/releases/tags/nightly":
					if mode == "missing" {
						w.WriteHeader(404)
						fmt.Fprint(w, `{"message":"Not Found"}`)
						return
					}
					tag := "nightly"
					if mode == "wrong-tag" {
						tag = "v14"
					}
					fmt.Fprintf(w, `{"tag_name":%q,"prerelease":%t,"draft":%t,"html_url":"https://github.com/owner/repo/releases/tag/nightly","assets":[{"name":"Metrolist.apk","browser_download_url":"https://example.com/Metrolist.apk"}]}`, tag, mode != "stable", mode == "draft")
				default:
					t.Errorf("unexpected API path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			url := server.URL + "/"
			api, err := gh.NewClient(gh.WithHTTPClient(server.Client()), gh.WithURLs(&url, nil))
			if err != nil {
				t.Fatal(err)
			}
			client := &ReleasesClient{client: api, owner: "owner", repo: "repo"}
			result, err := client.GetNightly(context.Background())
			if mode == "private" || mode == "draft" || mode == "stable" || mode == "wrong-tag" {
				if err == nil {
					t.Fatal("expected rejection", result)
				}
				if mode == "private" && (calls != 1 || !strings.Contains(err.Error(), "private")) {
					t.Fatal("private data requested", calls, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing" {
				if result != nil {
					t.Fatal(result)
				}
				return
			}
			result.Assets[0].Name = "mutated"
			cached, err := client.GetNightly(context.Background())
			if err != nil || cached.Assets[0].Name != "Metrolist.apk" || calls != 2 {
				t.Fatal("cache lost isolation", cached, calls, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := client.GetNightly(ctx); err == nil {
				t.Fatal("cache ignored cancellation")
			}
		})
	}
}
