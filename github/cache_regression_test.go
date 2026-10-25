package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gh "github.com/google/go-github/v60/github"
)

func TestIssueSearchCannotReadPrivateIssues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("issue search received private repository access")
			fmt.Fprint(w, `{"items":[{"title":"private secret"}]}`)
			return
		}
		fmt.Fprint(w, `{"items":[]}`)
	}))
	defer server.Close()
	client := NewAssistantClient("read-token", "owner", "repo")
	client.apiBase = server.URL
	result, err := client.SearchIssues(context.Background(), "repo:owner/private OR secret")
	if err != nil || strings.Contains(result, "private secret") {
		t.Fatalf("search = %q, %v", result, err)
	}
}

func TestInvalidGitHubPathsNeverReachNetwork(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"private":false}`)
	}))
	defer server.Close()
	client := NewAssistantClient("read-token", "owner", "repo")
	client.apiBase = server.URL
	ctx := context.Background()
	for _, repository := range []string{"../repo", "owner/..", "owner/%2e%2e", "owner/name?other"} {
		if _, err := client.Repository(ctx, repository); err == nil {
			t.Errorf("accepted repository %q", repository)
		}
	}
	for _, path := range []string{"../private", "a/../../private", "./README.md"} {
		if _, err := client.File(ctx, "owner/repo", path, ""); err == nil {
			t.Errorf("accepted file path %q", path)
		}
	}
	if _, err := client.User(ctx, "../user"); err == nil {
		t.Error("accepted user path")
	}
	if _, err := client.Commits(ctx, "owner/repo", -1); err == nil {
		t.Error("accepted negative commit limit")
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid input made %d requests", calls.Load())
	}
}

func TestGitHubCachesReturnIndependentValues(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/repos/owner/repo" {
			fmt.Fprint(w, `{"private":false}`)
		} else if strings.HasSuffix(r.URL.Path, "/runs") {
			fmt.Fprint(w, `{"workflow_runs":[{"run_number":1,"display_title":"original","conclusion":"success"}]}`)
		} else {
			fmt.Fprint(w, `[{"tag_name":"v1","assets":[{"name":"original"}]}]`)
		}
	}))
	defer server.Close()
	api := gh.NewClient(server.Client())
	api.BaseURL, _ = url.Parse(server.URL + "/")
	actions := &ActionsClient{client: api, owner: "owner", repo: "repo", workflowFile: "build.yml"}
	releases := &ReleasesClient{client: api, owner: "owner", repo: "repo"}
	ctx := context.Background()
	firstRuns, err := actions.FetchRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstRuns.Runs[0].Title = "changed"
	firstRelease, err := releases.GetRelease(ctx, "latest")
	if err != nil {
		t.Fatal(err)
	}
	firstRelease.TagName, firstRelease.Assets[0].Name = "changed", "changed"
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			runs, err := actions.FetchRuns(ctx)
			if err != nil || runs.Runs[0].Title != "original" {
				t.Errorf("cached runs = %+v, %v", runs, err)
				return
			}
			runs.Runs[0].Title = "concurrent mutation"
			release, err := releases.GetRelease(ctx, "latest")
			if err != nil || release.TagName != "v1" || release.Assets[0].Name != "original" {
				t.Errorf("cached release = %+v, %v", release, err)
				return
			}
			release.Assets[0].Name = "concurrent mutation"
		})
	}
	wg.Wait()
	if calls.Load() != 4 {
		t.Fatalf("requests = %d, want 4 including visibility checks", calls.Load())
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := actions.FetchRuns(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached runs cancellation = %v", err)
	}
	if _, err := releases.FetchReleases(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached releases cancellation = %v", err)
	}
	// Version data still refreshes after its existing TTL.
	releases.fetchedAt = time.Now().Add(-6 * time.Minute)
	if _, err := releases.FetchReleases(ctx); err != nil || calls.Load() != 6 {
		t.Fatalf("expired releases: calls=%d, err=%v", calls.Load(), err)
	}
}

func TestEmptyReleaseListIsCached(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/repos/owner/repo" {
			fmt.Fprint(w, `{"private":false}`)
		} else {
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	api := gh.NewClient(server.Client())
	api.BaseURL, _ = url.Parse(server.URL + "/")
	client := &ReleasesClient{client: api, owner: "owner", repo: "repo"}
	for range 2 {
		if releases, err := client.FetchReleases(context.Background()); err != nil || len(releases) != 0 {
			t.Fatalf("releases = %v, %v", releases, err)
		}
	}
	if calls != 2 {
		t.Fatalf("requests = %d, want 2 including visibility check", calls)
	}
}

func TestActionsAndReleasesRejectPrivateRepositories(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repos/owner/private" {
			t.Errorf("private contents requested: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"private":true}`)
	}))
	defer server.Close()
	api := gh.NewClient(server.Client())
	api.BaseURL, _ = url.Parse(server.URL + "/")
	ctx := context.Background()
	actions := &ActionsClient{client: api, owner: "owner", repo: "private", workflowFile: "build.yml"}
	if _, err := actions.FetchRuns(ctx); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("private runs error = %v", err)
	}
	releases := &ReleasesClient{client: api, owner: "owner", repo: "private"}
	if _, err := releases.FetchReleases(ctx); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("private releases error = %v", err)
	}
	if calls != 2 || actions.cached != nil || releases.cached != nil {
		t.Fatalf("private data fetched or cached: %d calls", calls)
	}
}

type assistantTransport func(*http.Request) (*http.Response, error)

func (f assistantTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubSDKResponsesAreBounded(t *testing.T) {
	transport := boundedGitHubTransport{base: assistantTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (8<<20)+1)))}, nil
	})}
	request, _ := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err == nil || len(body) != 8<<20 {
		t.Fatalf("body size=%d err=%v", len(body), err)
	}
}

func TestAssistantRejectsOversizedBodiesAndCrossOriginRedirects(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(fmt.Sprint(redirect), func(t *testing.T) {
			client := NewAssistantClient("read-token", "owner", "repo")
			calls := 0
			client.httpClient.Transport = assistantTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"login":"nyx"}` + strings.Repeat(" ", 512<<10))), Request: r}
				if redirect {
					response.StatusCode = 302
					response.Header.Set("Location", "https://sub.api.github.com/users/nyx")
				}
				return response, nil
			})
			_, err := client.User(context.Background(), "nyx")
			if err == nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}
