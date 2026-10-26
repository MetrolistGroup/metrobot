package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"
	"golang.org/x/oauth2"
)

type ActionsClient struct {
	client       *github.Client
	owner        string
	repo         string
	workflowFile string

	mu        sync.Mutex
	cached    *ActionsResult
	fetchedAt time.Time
}

type ActionsResult struct {
	Runs []WorkflowRunInfo
}

type WorkflowRunInfo struct {
	RunNumber  int
	Status     string // "in_progress", "queued", "completed"
	Conclusion string // "success", "failure", "cancelled", ""
	Title      string
	HeadSHA    string
}

func NewActionsClient(token, owner, repo, workflowFile string) (*ActionsClient, error) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	tc.Timeout = 15 * time.Second
	tc.CheckRedirect = sameOriginRedirect
	tc.Transport = boundedGitHubTransport{base: tc.Transport}
	client, err := github.NewClient(github.WithHTTPClient(tc))
	if err != nil {
		return nil, fmt.Errorf("creating GitHub Actions client: %w", err)
	}

	return &ActionsClient{
		client:       client,
		owner:        owner,
		repo:         repo,
		workflowFile: workflowFile,
	}, nil
}

func (a *ActionsClient) FetchRuns(ctx context.Context) (*ActionsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.cached != nil && time.Since(a.fetchedAt) < 20*time.Minute {
		result := &ActionsResult{Runs: slices.Clone(a.cached.Runs)}
		a.mu.Unlock()
		return result, nil
	}
	a.mu.Unlock()

	client, err := publicRepositoryClient(ctx, a.client, a.owner, a.repo)
	if err != nil {
		return nil, err
	}
	runs, _, err := client.Actions.ListWorkflowRunsByFileName(
		ctx, a.owner, a.repo, a.workflowFile,
		&github.ListWorkflowRunsOptions{
			Branch:      "main",
			ListOptions: github.ListOptions{PerPage: 20},
		},
	)
	if isForbiddenGitHubError(err) {
		client, err = newPublicGitHubClient()
		if err != nil {
			return nil, err
		}
		runs, _, err = client.Actions.ListWorkflowRunsByFileName(
			ctx, a.owner, a.repo, a.workflowFile,
			&github.ListWorkflowRunsOptions{
				Branch:      "main",
				ListOptions: github.ListOptions{PerPage: 20},
			},
		)
	}
	if err != nil {
		return nil, fmt.Errorf("fetching workflow runs: %w", err)
	}

	var infos []WorkflowRunInfo
	foundSuccess := false
	for _, run := range runs.WorkflowRuns {
		title := strings.TrimSpace(run.GetDisplayTitle())
		if title == "" && run.GetHeadCommit() != nil {
			title = firstLine(strings.TrimSpace(run.GetHeadCommit().GetMessage()))
		}
		if title == "" {
			title = shortSHA(run.GetHeadSHA())
		}

		info := WorkflowRunInfo{
			RunNumber:  run.GetRunNumber(),
			Status:     run.GetStatus(),
			Conclusion: run.GetConclusion(),
			Title:      title,
			HeadSHA:    shortSHA(run.GetHeadSHA()),
		}
		infos = append(infos, info)

		if run.GetConclusion() == "success" {
			foundSuccess = true
			break
		}
	}

	_ = foundSuccess

	result := &ActionsResult{Runs: infos}

	a.mu.Lock()
	a.cached = &ActionsResult{Runs: slices.Clone(result.Runs)}
	a.fetchedAt = time.Now()
	a.mu.Unlock()

	return result, nil
}

type ReleaseInfo struct {
	TagName     string
	PublishedAt time.Time
	Body        string
	HTMLURL     string
	Assets      []ReleaseAsset
}

type ReleaseAsset struct {
	Name          string
	DownloadURL   string
	Size          int
	DownloadCount int
}

type ReleasesClient struct {
	client *github.Client
	owner  string
	repo   string

	mu        sync.Mutex
	cached    []*ReleaseInfo
	fetchedAt time.Time
}

func NewReleasesClient(token, owner, repo string) (*ReleasesClient, error) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	tc.Timeout = 15 * time.Second
	tc.CheckRedirect = sameOriginRedirect
	tc.Transport = boundedGitHubTransport{base: tc.Transport}
	client, err := github.NewClient(github.WithHTTPClient(tc))
	if err != nil {
		return nil, fmt.Errorf("creating GitHub releases client: %w", err)
	}

	return &ReleasesClient{
		client: client,
		owner:  owner,
		repo:   repo,
	}, nil
}

func (r *ReleasesClient) FetchReleases(ctx context.Context) ([]*ReleaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.cached != nil && time.Since(r.fetchedAt) < 5*time.Minute {
		result := cloneReleases(r.cached)
		r.mu.Unlock()
		return result, nil
	}
	r.mu.Unlock()

	client, err := publicRepositoryClient(ctx, r.client, r.owner, r.repo)
	if err != nil {
		return nil, err
	}
	releases, _, err := client.Repositories.ListReleases(
		ctx, r.owner, r.repo,
		&github.ListOptions{PerPage: 50},
	)
	if isForbiddenGitHubError(err) {
		client, err = newPublicGitHubClient()
		if err != nil {
			return nil, err
		}
		releases, _, err = client.Repositories.ListReleases(
			ctx, r.owner, r.repo,
			&github.ListOptions{PerPage: 50},
		)
	}
	if err != nil {
		return nil, fmt.Errorf("fetching releases: %w", err)
	}

	infos := make([]*ReleaseInfo, 0, len(releases))
	for _, rel := range releases {
		info := &ReleaseInfo{
			TagName:     rel.GetTagName(),
			PublishedAt: rel.GetPublishedAt().Time,
			Body:        rel.GetBody(),
			HTMLURL:     rel.GetHTMLURL(),
		}
		for _, asset := range rel.Assets {
			info.Assets = append(info.Assets, ReleaseAsset{
				Name:          asset.GetName(),
				DownloadURL:   asset.GetBrowserDownloadURL(),
				Size:          asset.GetSize(),
				DownloadCount: asset.GetDownloadCount(),
			})
		}
		infos = append(infos, info)
	}

	r.mu.Lock()
	r.cached = cloneReleases(infos)
	r.fetchedAt = time.Now()
	r.mu.Unlock()

	return infos, nil
}

func publicRepositoryClient(ctx context.Context, client *github.Client, owner, repo string) (*github.Client, error) {
	repository, _, err := client.Repositories.Get(ctx, owner, repo)
	if isForbiddenGitHubError(err) {
		client, err = newPublicGitHubClient()
		if err != nil {
			return nil, err
		}
		repository, _, err = client.Repositories.Get(ctx, owner, repo)
	}
	if err != nil {
		return nil, fmt.Errorf("checking repository visibility: %w", err)
	}
	if repository.GetPrivate() {
		return nil, fmt.Errorf("private repository details are not allowed")
	}
	return client, nil
}

func cloneReleases(releases []*ReleaseInfo) []*ReleaseInfo {
	result := make([]*ReleaseInfo, len(releases))
	for i, release := range releases {
		copy := *release
		copy.Assets = slices.Clone(release.Assets)
		result[i] = &copy
	}
	return result
}

func isForbiddenGitHubError(err error) bool {
	var responseError *github.ErrorResponse
	if !errors.As(err, &responseError) || responseError.Response == nil {
		return false
	}
	return responseError.Response.StatusCode == http.StatusUnauthorized || responseError.Response.StatusCode == http.StatusForbidden
}

type boundedGitHubTransport struct {
	base http.RoundTripper
}

func (t boundedGitHubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err == nil {
		response.Body = http.MaxBytesReader(nil, response.Body, 8<<20)
	}
	return response, err
}

func newPublicGitHubClient() (*github.Client, error) {
	return github.NewClient(github.WithHTTPClient(&http.Client{
		Timeout: 15 * time.Second, CheckRedirect: sameOriginRedirect,
		Transport: boundedGitHubTransport{base: http.DefaultTransport},
	}))
}

func firstLine(s string) string {
	if s == "" {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (r *ReleasesClient) GetRelease(ctx context.Context, tag string) (*ReleaseInfo, error) {
	releases, err := r.FetchReleases(ctx)
	if err != nil {
		return nil, err
	}

	if tag == "latest" || tag == "" {
		if len(releases) == 0 {
			return nil, nil
		}
		return releases[0], nil
	}

	for _, rel := range releases {
		if rel.TagName == tag {
			return rel, nil
		}
	}
	return nil, nil
}
