package github

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v60/github"
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

func NewActionsClient(token, owner, repo, workflowFile string) *ActionsClient {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	client := github.NewClient(tc)

	return &ActionsClient{
		client:       client,
		owner:        owner,
		repo:         repo,
		workflowFile: workflowFile,
	}
}

func (a *ActionsClient) FetchRuns(ctx context.Context) (*ActionsResult, error) {
	a.mu.Lock()
	if a.cached != nil && time.Since(a.fetchedAt) < 20*time.Minute {
		result := a.cached
		a.mu.Unlock()
		return result, nil
	}
	a.mu.Unlock()

	runs, _, err := a.client.Actions.ListWorkflowRunsByFileName(
		ctx, a.owner, a.repo, a.workflowFile,
		&github.ListWorkflowRunsOptions{
			Branch:      "main",
			ListOptions: github.ListOptions{PerPage: 20},
		},
	)
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
	a.cached = result
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

func NewReleasesClient(token, owner, repo string) *ReleasesClient {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	client := github.NewClient(tc)

	return &ReleasesClient{
		client: client,
		owner:  owner,
		repo:   repo,
	}
}

func (r *ReleasesClient) FetchReleases(ctx context.Context) ([]*ReleaseInfo, error) {
	r.mu.Lock()
	if r.cached != nil && time.Since(r.fetchedAt) < 5*time.Minute {
		result := r.cached
		r.mu.Unlock()
		return result, nil
	}
	r.mu.Unlock()

	releases, _, err := r.client.Repositories.ListReleases(
		ctx, r.owner, r.repo,
		&github.ListOptions{PerPage: 50},
	)
	if err != nil {
		return nil, fmt.Errorf("fetching releases: %w", err)
	}

	var infos []*ReleaseInfo
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
	r.cached = infos
	r.fetchedAt = time.Now()
	r.mu.Unlock()

	return infos, nil
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
