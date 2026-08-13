package github

import (
	"net/http"
	"testing"

	goGitHub "github.com/google/go-github/v60/github"
)

func TestIsForbiddenGitHubError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := &goGitHub.ErrorResponse{Response: &http.Response{StatusCode: status}}
		if !isForbiddenGitHubError(err) {
			t.Errorf("status %d was not retryable", status)
		}
	}
}
