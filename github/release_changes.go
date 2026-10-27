package github

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type githubReleaseEvidence struct {
	Tag        string     `json:"tag_name"`
	URL        string     `json:"html_url"`
	Target     string     `json:"target_commitish"`
	Published  *time.Time `json:"published_at,omitempty"`
	Draft      bool       `json:"draft"`
	Prerelease bool       `json:"prerelease"`
	Assets     []struct {
		Name  string `json:"name"`
		State string `json:"state"`
		URL   string `json:"browser_download_url"`
	} `json:"assets,omitempty"`
}

type githubComparison struct {
	URL     string         `json:"html_url"`
	Status  string         `json:"status"`
	Total   int            `json:"total_commits"`
	Base    githubCommit   `json:"base_commit"`
	Commits []githubCommit `json:"commits"`
}

// ChangesSinceRelease reads a pinned main or published-nightly snapshot, not just tip commits.
func (c *AssistantClient) ChangesSinceRelease(ctx context.Context, ref, query string) (string, error) {
	ref, query = strings.ToLower(strings.TrimSpace(ref)), strings.TrimSpace(query)
	if ref == "" {
		ref = "main"
	}
	if (ref != "main" && ref != "nightly") || len(query) > 100 {
		return "", fmt.Errorf("ref must be main or nightly and query must be at most 100 bytes")
	}
	repository, err := c.Repository(ctx, c.owner+"/"+c.repo)
	if err != nil {
		return "", err
	}
	var metadata githubRepositoryResult
	if err := json.Unmarshal([]byte(repository), &metadata); err != nil {
		return "", err
	}
	basePath := "/repos/" + url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo)
	var stable githubReleaseEvidence
	if err := c.get(ctx, basePath+"/releases/latest", &stable); err != nil {
		return "", err
	}
	if stable.Tag == "" || stable.Draft || stable.Prerelease {
		return "", fmt.Errorf("latest stable release could not be verified")
	}
	stable.Assets = nil
	var nightly *githubReleaseEvidence
	var head string
	if ref == "nightly" {
		nightly = &githubReleaseEvidence{}
		if err := c.get(ctx, basePath+"/releases/tags/nightly", nightly); err != nil {
			return "", err
		}
		if nightly.Tag != "nightly" || nightly.Draft || !validGitHubSHA(nightly.Target) {
			return "", fmt.Errorf("published nightly has no verified immutable build commit")
		}
		head, err = c.tagCommit(ctx, basePath, "nightly")
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(head, nightly.Target) {
			return "", fmt.Errorf("nightly tag does not match its declared build commit")
		}
		assets := nightly.Assets[:0]
		for _, asset := range nightly.Assets {
			if asset.State == "uploaded" && asset.URL != "" && strings.HasSuffix(strings.ToLower(asset.Name), ".apk") {
				assets = append(assets, asset)
			}
		}
		nightly.Assets = assets
		if len(assets) == 0 {
			return "", fmt.Errorf("nightly has no published uploaded APK")
		}
	} else {
		if metadata.DefaultBranch == "" {
			return "", fmt.Errorf("default branch is unavailable")
		}
		var branch struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		if err := c.get(ctx, basePath+"/git/ref/heads/"+url.PathEscape(metadata.DefaultBranch), &branch); err != nil {
			return "", err
		}
		head = branch.Object.SHA
	}
	if !validGitHubSHA(head) {
		return "", fmt.Errorf("target commit is unavailable")
	}
	var comparison githubComparison
	comparePath := basePath + "/compare/" + url.PathEscape(stable.Tag) + "..." + head
	// GitHub includes changed-file patches on page one even though we only return commits.
	if err := c.getLimit(ctx, comparePath+"?per_page=100&page=1", &comparison, 2<<20); err != nil {
		return "", err
	}
	if !validGitHubSHA(comparison.Base.SHA) || comparison.Total < len(comparison.Commits) || comparison.Total < 0 {
		return "", fmt.Errorf("invalid release comparison")
	}
	pages := max(1, (comparison.Total+99)/100)
	// shortcut: scan three newest pages; add a cursor if older fixes fall outside this window.
	firstPage := max(1, pages-2)
	commits := comparison.Commits
	if firstPage > 1 {
		commits = nil
	}
	for page := max(2, firstPage); page <= pages; page++ {
		var next githubComparison
		path := fmt.Sprintf("%s/compare/%s...%s?per_page=100&page=%d", basePath, comparison.Base.SHA, head, page)
		if err := c.getLimit(ctx, path, &next, 2<<20); err != nil {
			return "", err
		}
		if next.Total != comparison.Total || next.Base.SHA != comparison.Base.SHA {
			return "", fmt.Errorf("release comparison changed while paging")
		}
		commits = append(commits, next.Commits...)
	}
	type change struct {
		SHA     string `json:"sha"`
		Message string `json:"message"`
		URL     string `json:"url"`
		Partial bool   `json:"message_truncated"`
	}
	var matches []change
	for i := len(commits) - 1; i >= 0; i-- {
		commit := commits[i]
		if query == "" || strings.Contains(strings.ToLower(commit.Commit.Message), strings.ToLower(query)) {
			message := githubTextPrefix(commit.Commit.Message, 800)
			matches = append(matches, change{commit.SHA, message, commit.HTMLURL, len(message) < len(commit.Commit.Message)})
		}
	}
	matched := len(matches)
	matches = matches[:min(len(matches), 10)]
	result := struct {
		Stable  githubReleaseEvidence  `json:"latest_release"`
		BaseSHA string                 `json:"release_commit_sha"`
		Ref     string                 `json:"target_ref"`
		SHA     string                 `json:"target_sha"`
		Nightly *githubReleaseEvidence `json:"nightly_release,omitempty"`
		URL     string                 `json:"compare_url"`
		Status  string                 `json:"status"`
		Total   int                    `json:"total_commits"`
		Scanned int                    `json:"scanned_commits"`
		Partial bool                   `json:"history_truncated"`
		Matched int                    `json:"matching_commits"`
		More    bool                   `json:"matches_truncated"`
		Commits []change               `json:"commits"`
	}{stable, comparison.Base.SHA, ref, head, nightly, comparison.URL, comparison.Status, comparison.Total, len(commits), len(commits) < comparison.Total, matched, matched > len(matches), matches}
	for {
		output, err := marshalToolResult(result)
		if err != nil || len(output) <= 10<<10 {
			return output, err
		}
		if len(result.Commits) > 0 {
			result.Commits = result.Commits[:len(result.Commits)-1]
			result.More = true
		} else if nightly != nil && len(nightly.Assets) > 1 {
			nightly.Assets = nightly.Assets[:len(nightly.Assets)-1]
		} else {
			return "", fmt.Errorf("release metadata exceeds the tool budget")
		}
	}
}

// ProjectCommit exposes code evidence only from the configured public project.
func (c *AssistantClient) ProjectCommit(ctx context.Context, sha string) (string, error) {
	sha = strings.TrimSpace(sha)
	if !validGitHubSHA(sha) {
		return "", fmt.Errorf("a full 40-character commit SHA from public project evidence is required")
	}
	if _, err := c.Repository(ctx, c.owner+"/"+c.repo); err != nil {
		return "", err
	}
	var commit struct {
		githubCommit
		Files []struct {
			Name           string `json:"filename"`
			Status         string `json:"status"`
			Patch          string `json:"patch"`
			PatchTruncated bool   `json:"patch_truncated"`
		} `json:"files"`
	}
	path := "/repos/" + url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo) + "/commits/" + sha + "?per_page=5"
	if err := c.get(ctx, path, &commit); err != nil {
		return "", err
	}
	if !strings.EqualFold(commit.SHA, sha) {
		return "", fmt.Errorf("returned commit does not match the requested SHA")
	}
	messagePartial := len(commit.Commit.Message) > 2000
	commit.Commit.Message = githubTextPrefix(commit.Commit.Message, 2000)
	partial := len(commit.Files) >= 5
	commit.Files = commit.Files[:min(len(commit.Files), 5)]
	for i := range commit.Files {
		patch := commit.Files[i].Patch
		commit.Files[i].Patch = githubTextPrefix(patch, 1500)
		commit.Files[i].PatchTruncated = len(patch) > len(commit.Files[i].Patch)
	}
	for {
		output, err := marshalToolResult(struct {
			Commit         any  `json:"commit"`
			Partial        bool `json:"file_list_may_be_truncated"`
			MessagePartial bool `json:"message_truncated"`
		}{commit, partial, messagePartial})
		if err != nil || len(output) <= 10<<10 {
			return output, err
		}
		if len(commit.Files) > 1 {
			commit.Files = commit.Files[:len(commit.Files)-1]
			partial = true
		} else if len(commit.Files) == 1 && len(commit.Files[0].Patch) > 100 {
			commit.Files[0].Patch = githubTextPrefix(commit.Files[0].Patch, len(commit.Files[0].Patch)/2)
			commit.Files[0].PatchTruncated = true
		} else if len(commit.Commit.Message) > 500 {
			commit.Commit.Message = githubTextPrefix(commit.Commit.Message, len(commit.Commit.Message)/2)
			messagePartial = true
		} else {
			return "", fmt.Errorf("commit metadata exceeds the tool budget")
		}
	}
}

func (c *AssistantClient) tagCommit(ctx context.Context, basePath, tag string) (string, error) {
	type object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
	}
	var reference struct {
		Object object `json:"object"`
	}
	if err := c.get(ctx, basePath+"/git/ref/tags/"+url.PathEscape(tag), &reference); err != nil {
		return "", err
	}
	for range 5 {
		if !validGitHubSHA(reference.Object.SHA) {
			break
		}
		if reference.Object.Type == "commit" {
			return reference.Object.SHA, nil
		}
		if reference.Object.Type != "tag" {
			break
		}
		var annotation struct {
			Object object `json:"object"`
		}
		if err := c.get(ctx, basePath+"/git/tags/"+reference.Object.SHA, &annotation); err != nil {
			return "", err
		}
		reference.Object = annotation.Object
	}
	return "", fmt.Errorf("release tag does not resolve to a commit")
}

func validGitHubSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func githubTextPrefix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for !utf8.ValidString(text[:limit]) {
		limit--
	}
	return text[:limit]
}
