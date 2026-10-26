package github

import (
	"net/http"
	"testing"
	"time"

	goGitHub "github.com/google/go-github/v93/github"
)

func TestGitHubConstructorsPreserveHTTPPolicies(t *testing.T) {
	actions, err := NewActionsClient("test-token", "owner", "repo", "build.yml")
	if err != nil {
		t.Fatal(err)
	}
	releases, err := NewReleasesClient("test-token", "owner", "repo")
	if err != nil {
		t.Fatal(err)
	}
	public, err := newPublicGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	original, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/owner/repo", nil)
	redirect, _ := http.NewRequest(http.MethodGet, "https://other.example/", nil)
	for _, api := range []*goGitHub.Client{actions.client, releases.client, public} {
		client := api.Client()
		if client.Timeout != 15*time.Second || client.CheckRedirect == nil || client.CheckRedirect(redirect, []*http.Request{original}) == nil {
			t.Fatal("GitHub client lost its timeout or cross-origin redirect rejection")
		}
		if _, ok := client.Transport.(boundedGitHubTransport); !ok {
			t.Fatal("GitHub client lost its response body limit")
		}
	}
}

func TestIsForbiddenGitHubError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := &goGitHub.ErrorResponse{Response: &http.Response{StatusCode: status}}
		if !isForbiddenGitHubError(err) {
			t.Errorf("status %d was not retryable", status)
		}
	}
}
