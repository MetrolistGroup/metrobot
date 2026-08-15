package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const githubAPIBase = "https://api.github.com"

type AssistantClient struct {
	token      string
	owner      string
	repo       string
	httpClient *http.Client
	apiBase    string
}

func NewAssistantClient(token, owner, repo string) *AssistantClient {
	return &AssistantClient{
		token:      strings.TrimSpace(token),
		owner:      strings.TrimSpace(owner),
		repo:       strings.TrimSpace(repo),
		httpClient: &http.Client{Timeout: 12 * time.Second},
		apiBase:    githubAPIBase,
	}
}

func (c *AssistantClient) ProjectStatus(ctx context.Context) (string, error) {
	var repository struct {
		FullName        string     `json:"full_name"`
		Description     string     `json:"description"`
		HTMLURL         string     `json:"html_url"`
		Homepage        string     `json:"homepage"`
		Archived        bool       `json:"archived"`
		DefaultBranch   string     `json:"default_branch"`
		StargazersCount int        `json:"stargazers_count"`
		ForksCount      int        `json:"forks_count"`
		OpenIssuesCount int        `json:"open_issues_count"`
		PushedAt        *time.Time `json:"pushed_at"`
	}
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/%s", url.PathEscape(c.owner), url.PathEscape(c.repo)), &repository); err != nil {
		return "", err
	}

	var release struct {
		TagName     string     `json:"tag_name"`
		Name        string     `json:"name"`
		HTMLURL     string     `json:"html_url"`
		PublishedAt *time.Time `json:"published_at"`
		Body        string     `json:"body"`
	}
	warnings := make([]string, 0, 2)
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/releases/latest", url.PathEscape(c.owner), url.PathEscape(c.repo)), &release); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		warnings = append(warnings, "Latest release unavailable: "+err.Error())
		release = struct {
			TagName     string     `json:"tag_name"`
			Name        string     `json:"name"`
			HTMLURL     string     `json:"html_url"`
			PublishedAt *time.Time `json:"published_at"`
			Body        string     `json:"body"`
		}{}
	}

	var commits []struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
		Commit  struct {
			Message string `json:"message"`
			Author  struct {
				Name string     `json:"name"`
				Date *time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/commits?per_page=5", url.PathEscape(c.owner), url.PathEscape(c.repo)), &commits); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		warnings = append(warnings, "Recent commits unavailable: "+err.Error())
	}

	type commitSummary struct {
		SHA     string     `json:"sha"`
		Message string     `json:"message"`
		Author  string     `json:"author"`
		Date    *time.Time `json:"date,omitempty"`
		URL     string     `json:"url"`
	}
	recent := make([]commitSummary, 0, len(commits))
	for _, commit := range commits {
		sha := commit.SHA
		if len(sha) > 7 {
			sha = sha[:7]
		}
		message := strings.TrimSpace(strings.SplitN(commit.Commit.Message, "\n", 2)[0])
		recent = append(recent, commitSummary{
			SHA:     sha,
			Message: message,
			Author:  commit.Commit.Author.Name,
			Date:    commit.Commit.Author.Date,
			URL:     commit.HTMLURL,
		})
	}

	result := struct {
		Repository any             `json:"repository"`
		Release    any             `json:"latest_release,omitempty"`
		Commits    []commitSummary `json:"recent_commits,omitempty"`
		Warnings   []string        `json:"warnings,omitempty"`
	}{Repository: repository, Commits: recent, Warnings: warnings}
	if release.TagName != "" {
		result.Release = release
	}
	return marshalToolResult(result)
}

func (c *AssistantClient) SearchIssues(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("search query is required")
	}
	values := url.Values{
		"q":        {fmt.Sprintf("repo:%s/%s is:issue %s", c.owner, c.repo, query)},
		"sort":     {"updated"},
		"order":    {"desc"},
		"per_page": {"8"},
	}
	var search struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			Number    int        `json:"number"`
			Title     string     `json:"title"`
			State     string     `json:"state"`
			HTMLURL   string     `json:"html_url"`
			CreatedAt *time.Time `json:"created_at"`
			UpdatedAt *time.Time `json:"updated_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"items"`
	}
	if err := c.get(ctx, "/search/issues?"+values.Encode(), &search); err != nil {
		return "", err
	}
	return marshalToolResult(search)
}

func (c *AssistantClient) User(ctx context.Context, username string) (string, error) {
	username = strings.TrimSpace(strings.TrimPrefix(username, "@"))
	if username == "" {
		return "", fmt.Errorf("GitHub username is required")
	}
	var user struct {
		Login       string     `json:"login"`
		Name        string     `json:"name"`
		Bio         string     `json:"bio"`
		Company     string     `json:"company"`
		Location    string     `json:"location"`
		HTMLURL     string     `json:"html_url"`
		Type        string     `json:"type"`
		PublicRepos int        `json:"public_repos"`
		Followers   int        `json:"followers"`
		CreatedAt   *time.Time `json:"created_at"`
		UpdatedAt   *time.Time `json:"updated_at"`
	}
	if err := c.get(ctx, "/users/"+url.PathEscape(username), &user); err != nil {
		return "", err
	}
	return marshalToolResult(user)
}

func (c *AssistantClient) SearchRepositories(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("repository search query is required")
	}
	lower := strings.ToLower(query)
	if strings.Contains(lower, "is:private") || strings.Contains(lower, "visibility:private") {
		return "", fmt.Errorf("private repository search is not allowed")
	}
	if !strings.Contains(lower, "is:public") {
		query += " is:public"
	}
	values := url.Values{
		"q":        {query},
		"per_page": {"8"},
	}
	var search struct {
		TotalCount        int                `json:"total_count"`
		IncompleteResults bool               `json:"incomplete_results"`
		Items             []githubRepository `json:"items"`
	}
	if err := c.get(ctx, "/search/repositories?"+values.Encode(), &search); err != nil {
		return "", err
	}
	items := make([]githubRepositoryResult, 0, len(search.Items))
	for _, repository := range search.Items {
		if !repository.Private {
			items = append(items, repository.result())
		}
	}
	return marshalToolResult(struct {
		TotalCount        int                      `json:"total_count"`
		IncompleteResults bool                     `json:"incomplete_results"`
		Items             []githubRepositoryResult `json:"items"`
	}{search.TotalCount, search.IncompleteResults, items})
}

func (c *AssistantClient) Repository(ctx context.Context, repository string) (string, error) {
	owner, name, err := githubRepositoryName(repository, c.owner)
	if err != nil {
		return "", err
	}
	var result githubRepository
	if err := c.get(ctx, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), &result); err != nil {
		return "", err
	}
	if result.Private {
		return "", fmt.Errorf("private repository details are not allowed")
	}
	return marshalToolResult(result.result())
}

type githubRepository struct {
	FullName        string     `json:"full_name"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	HTMLURL         string     `json:"html_url"`
	Homepage        string     `json:"homepage"`
	Language        string     `json:"language"`
	Topics          []string   `json:"topics"`
	Visibility      string     `json:"visibility"`
	Private         bool       `json:"private"`
	Archived        bool       `json:"archived"`
	Disabled        bool       `json:"disabled"`
	Fork            bool       `json:"fork"`
	DefaultBranch   string     `json:"default_branch"`
	StargazersCount int        `json:"stargazers_count"`
	ForksCount      int        `json:"forks_count"`
	OpenIssuesCount int        `json:"open_issues_count"`
	CreatedAt       *time.Time `json:"created_at"`
	UpdatedAt       *time.Time `json:"updated_at"`
	PushedAt        *time.Time `json:"pushed_at"`
	Owner           struct {
		Login string `json:"login"`
	} `json:"owner"`
	License *struct {
		SPDXID string `json:"spdx_id"`
		Name   string `json:"name"`
	} `json:"license"`
}

type githubRepositoryResult struct {
	FullName      string     `json:"full_name"`
	Name          string     `json:"name"`
	Owner         string     `json:"owner"`
	Description   string     `json:"description"`
	URL           string     `json:"url"`
	Homepage      string     `json:"homepage,omitempty"`
	Language      string     `json:"language,omitempty"`
	Topics        []string   `json:"topics,omitempty"`
	Visibility    string     `json:"visibility,omitempty"`
	Archived      bool       `json:"archived"`
	Disabled      bool       `json:"disabled"`
	Fork          bool       `json:"fork"`
	DefaultBranch string     `json:"default_branch"`
	Stars         int        `json:"stars"`
	Forks         int        `json:"forks"`
	OpenIssues    int        `json:"open_issues"`
	License       string     `json:"license,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	PushedAt      *time.Time `json:"pushed_at,omitempty"`
}

func (r githubRepository) result() githubRepositoryResult {
	license := ""
	if r.License != nil {
		license = r.License.SPDXID
		if license == "" || license == "NOASSERTION" {
			license = r.License.Name
		}
	}
	return githubRepositoryResult{
		FullName: r.FullName, Name: r.Name, Owner: r.Owner.Login, Description: r.Description,
		URL: r.HTMLURL, Homepage: r.Homepage, Language: r.Language, Topics: r.Topics,
		Visibility: r.Visibility, Archived: r.Archived, Disabled: r.Disabled, Fork: r.Fork,
		DefaultBranch: r.DefaultBranch, Stars: r.StargazersCount, Forks: r.ForksCount,
		OpenIssues: r.OpenIssuesCount, License: license, CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt, PushedAt: r.PushedAt,
	}
}

func githubRepositoryName(repository, defaultOwner string) (string, string, error) {
	repository = strings.TrimSpace(repository)
	if parsed, err := url.Parse(repository); err == nil && parsed.Host != "" {
		if !strings.EqualFold(parsed.Host, "github.com") && !strings.EqualFold(parsed.Host, "www.github.com") {
			return "", "", fmt.Errorf("repository URL must use github.com")
		}
		repository = parsed.Path
	}
	repository = strings.Trim(strings.TrimSpace(repository), "/")
	repository = strings.TrimSuffix(repository, ".git")
	parts := strings.Split(repository, "/")
	if len(parts) == 1 && strings.TrimSpace(defaultOwner) != "" {
		parts = []string{strings.TrimSpace(defaultOwner), parts[0]}
	}
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" ||
		strings.ContainsAny(parts[0]+parts[1], " \t\r\n") {
		return "", "", fmt.Errorf("repository must be an owner/name or github.com URL")
	}
	return parts[0], parts[1], nil
}

func (c *AssistantClient) get(ctx context.Context, path string, target any) error {
	status, body, err := c.request(ctx, path, c.token)
	if err != nil {
		return err
	}
	// Invalid or organization-restricted tokens should not break public data.
	if (status == http.StatusUnauthorized || status == http.StatusForbidden) && c.token != "" {
		status, body, err = c.request(ctx, path, "")
		if err != nil {
			return err
		}
	}
	if status < 200 || status >= 300 {
		var responseError struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &responseError)
		message := strings.TrimSpace(responseError.Message)
		if message == "" {
			message = http.StatusText(status)
		}
		return fmt.Errorf("GitHub returned %d: %s", status, message)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decoding GitHub response: %w", err)
	}
	return nil
}

func (c *AssistantClient) request(ctx context.Context, path, token string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.apiBase, "/")+path, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("creating GitHub request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Metrobot-Garmin")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("calling GitHub: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 512<<10))
	if err != nil {
		return 0, nil, fmt.Errorf("reading GitHub response: %w", err)
	}
	return response.StatusCode, body, nil
}

func marshalToolResult(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encoding tool result: %w", err)
	}
	return string(data), nil
}
