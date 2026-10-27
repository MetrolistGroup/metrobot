package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChangesSinceReleaseFindsOlderFixAndPinsNightly(t *testing.T) {
	for _, count := range []int{113, 450} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
			commits := make([]githubCommit, count)
			for i := range commits {
				commits[i].SHA = fmt.Sprintf("%040x", i+1)
				commits[i].Commit.Message = "Translation update"
				commits[i].HTMLURL = "https://github.com/owner/repo/commit/" + commits[i].SHA
			}
			commits[10].Commit.Message = "fix(downloads): restore unthrottled range requests"
			if count > 300 {
				commits[count-1].Commit.Message = "fix(downloads): recent improvement"
			}
			var pages []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/repos/owner/repo":
					fmt.Fprint(w, `{"private":false,"default_branch":"main"}`)
				case r.URL.Path == "/repos/owner/repo/releases/latest":
					fmt.Fprint(w, `{"tag_name":"v1","html_url":"https://github.com/owner/repo/releases/tag/v1"}`)
				case r.URL.Path == "/repos/owner/repo/git/ref/heads/main":
					fmt.Fprintf(w, `{"object":{"sha":%q}}`, head)
				case r.URL.Path == "/repos/owner/repo/git/ref/tags/nightly":
					fmt.Fprintf(w, `{"object":{"type":"commit","sha":%q}}`, head)
				case r.URL.Path == "/repos/owner/repo/releases/tags/nightly":
					fmt.Fprintf(w, `{"tag_name":"nightly","target_commitish":%q,"prerelease":true,"assets":[{"name":"app.apk","state":"uploaded","browser_download_url":"https://github.com/owner/repo/releases/download/nightly/app.apk"}]}`, head)
				case strings.Contains(r.URL.Path, "/compare/"):
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					pages = append(pages, page)
					wantBase := base
					if page == 1 {
						wantBase = "v1"
					}
					if r.URL.Path != "/repos/owner/repo/compare/"+wantBase+"..."+head || r.URL.Query().Get("per_page") != "100" {
						t.Errorf("comparison was not pinned: %s", r.URL.String())
					}
					lo, hi := (page-1)*100, min(page*100, count)
					payload := map[string]any{"status": "ahead", "total_commits": count, "base_commit": githubCommit{SHA: base}, "commits": commits[lo:hi]}
					if page == 1 {
						// Real GitHub comparisons can exceed the ordinary endpoint's 512 KiB budget.
						payload["files"] = []map[string]string{{"patch": strings.Repeat("x", 600<<10)}}
					}
					_ = json.NewEncoder(w).Encode(payload)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client := NewAssistantClient("", "owner", "repo")
			client.apiBase, client.httpClient = server.URL, server.Client()
			output, err := client.ChangesSinceRelease(context.Background(), "nightly", "download")
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				SHA     string `json:"target_sha"`
				Base    string `json:"release_commit_sha"`
				Total   int    `json:"total_commits"`
				Scanned int    `json:"scanned_commits"`
				Partial bool   `json:"history_truncated"`
				Commits []struct {
					Message string `json:"message"`
				} `json:"commits"`
			}
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatal(err)
			}
			if result.SHA != head || result.Base != base || result.Total != count || len(result.Commits) != 1 || !strings.Contains(output, `"nightly_release"`) {
				t.Fatalf("bad release evidence: %s", output)
			}
			if count == 113 && (result.Partial || result.Scanned != 113 || len(pages) != 2 || !strings.Contains(result.Commits[0].Message, "unthrottled")) {
				t.Fatalf("lost a fix behind tip translations: %+v, pages %v", result, pages)
			}
			if count == 450 && (!result.Partial || result.Scanned != 250 || fmt.Sprint(pages) != "[1 3 4 5]" || !strings.Contains(result.Commits[0].Message, "recent")) {
				t.Fatalf("window coverage incorrect: %+v, pages %v", result, pages)
			}
			main, err := client.ChangesSinceRelease(context.Background(), "", "download")
			if err != nil || !strings.Contains(main, `"target_ref":"main"`) || strings.Contains(main, `"nightly_release"`) {
				t.Fatalf("main history was confused with a published nightly: %s, %v", main, err)
			}
		})
	}
}

func TestReleaseEvidenceRejectsUnverifiedAndOversizedData(t *testing.T) {
	for _, scenario := range []string{"private", "missing-release", "mutable-nightly", "wrong-tag", "missing-apk", "draft", "oversized-compare", "ordinary-limit"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch r.URL.Path {
				case "/repos/owner/repo":
					fmt.Fprintf(w, `{"private":%t,"default_branch":"main"}`, scenario == "private")
				case "/repos/owner/repo/releases/latest":
					if scenario == "missing-release" {
						w.WriteHeader(404)
						return
					}
					fmt.Fprint(w, `{"tag_name":"v1"}`)
				case "/repos/owner/repo/releases/tags/nightly":
					head := strings.Repeat("b", 40)
					if scenario == "mutable-nightly" {
						head = "main"
					}
					state := "uploaded"
					if scenario == "missing-apk" {
						state = "new"
					}
					fmt.Fprintf(w, `{"tag_name":"nightly","target_commitish":%q,"draft":%t,"assets":[{"name":"app.apk","state":%q,"browser_download_url":"https://github.com/owner/repo/releases/download/nightly/app.apk"}]}`, head, scenario == "draft", state)
				case "/repos/owner/repo/git/ref/tags/nightly":
					head := strings.Repeat("b", 40)
					if scenario == "wrong-tag" {
						head = strings.Repeat("c", 40)
					}
					fmt.Fprintf(w, `{"object":{"type":"commit","sha":%q}}`, head)
				default:
					limit := 2 << 20
					if scenario == "ordinary-limit" {
						limit = 600 << 10
					}
					fmt.Fprintf(w, `{"padding":%q}`, strings.Repeat("x", limit))
				}
			}))
			defer server.Close()
			client := NewAssistantClient("", "owner", "repo")
			client.apiBase, client.httpClient = server.URL, server.Client()
			var err error
			if scenario == "ordinary-limit" {
				var out any
				err = client.get(context.Background(), "/large", &out)
			} else {
				_, err = client.ChangesSinceRelease(context.Background(), "nightly", "")
			}
			if err == nil {
				t.Fatal("unverified or oversized source accepted")
			}
			if scenario == "oversized-compare" && !strings.Contains(err.Error(), "2048 KiB") {
				t.Fatalf("comparison size check not exercised: %v", err)
			}
			if scenario == "private" && requests != 1 {
				t.Fatal("read private release data after failed visibility check")
			}
		})
	}
}

func TestAnnotatedNightlyTagResolvesToBuildCommit(t *testing.T) {
	tag, head := strings.Repeat("c", 40), strings.Repeat("b", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/git/ref/tags/nightly":
			fmt.Fprintf(w, `{"object":{"type":"tag","sha":%q}}`, tag)
		case "/repos/owner/repo/git/tags/" + tag:
			fmt.Fprintf(w, `{"object":{"type":"commit","sha":%q}}`, head)
		default:
			t.Errorf("unexpected tag read %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := NewAssistantClient("", "owner", "repo")
	client.apiBase, client.httpClient = server.URL, server.Client()
	if sha, err := client.tagCommit(context.Background(), "/repos/owner/repo", "nightly"); err != nil || sha != head {
		t.Fatalf("tag=%s err=%v", sha, err)
	}
}

func TestReleaseChangesRemainBudgetedJSON(t *testing.T) {
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo":
			fmt.Fprint(w, `{"private":false,"default_branch":"main"}`)
		case "/repos/owner/repo/releases/latest":
			fmt.Fprint(w, `{"tag_name":"v1"}`)
		case "/repos/owner/repo/git/ref/heads/main":
			fmt.Fprintf(w, `{"object":{"sha":%q}}`, head)
		default:
			commits := make([]githubCommit, 13)
			for i := range commits {
				commits[i].SHA = fmt.Sprintf("%040x", i+1)
				commits[i].Commit.Message = "download " + strings.Repeat("<>", 1500)
			}
			_ = json.NewEncoder(w).Encode(githubComparison{Total: 13, Base: githubCommit{SHA: base}, Commits: commits})
		}
	}))
	defer server.Close()
	client := NewAssistantClient("", "owner", "repo")
	client.apiBase, client.httpClient = server.URL, server.Client()
	out, err := client.ChangesSinceRelease(context.Background(), "main", "download")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		More    bool `json:"matches_truncated"`
		Commits []struct {
			Partial bool `json:"message_truncated"`
		} `json:"commits"`
	}
	if len(out) > 10<<10 || json.Unmarshal([]byte(out), &result) != nil || !result.More || len(result.Commits) == 0 || len(result.Commits) >= 10 {
		t.Fatal("long changes escaped the JSON budget or lost coverage flags")
	}
	for _, c := range result.Commits {
		if !c.Partial {
			t.Fatal("message clipping was hidden")
		}
	}
}

func TestProjectCommitValidationAndPatchBounds(t *testing.T) {
	sha := strings.Repeat("b", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/escaped")
		if path == "/repos/owner/repo" {
			fmt.Fprint(w, `{"private":false}`)
			return
		}
		if path != "/repos/owner/repo/commits/"+sha || r.URL.Query().Get("per_page") != "5" {
			t.Errorf("unexpected commit lookup %s", r.URL.String())
		}
		files := make([]map[string]string, 6)
		patch := strings.Repeat("é", 1000)
		if strings.HasPrefix(r.URL.Path, "/escaped") {
			patch = strings.Repeat("<>", 1000)
		}
		for i := range files {
			files[i] = map[string]string{"filename": "download.kt", "patch": patch}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha, "commit": map[string]string{"message": "Restore download range requests"}, "files": files})
	}))
	defer server.Close()
	client := NewAssistantClient("", "owner", "repo")
	client.apiBase, client.httpClient = server.URL, server.Client()
	for _, invalid := range []string{"main", "../secret", strings.Repeat("g", 40), sha[:7]} {
		if _, err := client.ProjectCommit(context.Background(), invalid); err == nil {
			t.Fatalf("accepted invalid SHA %q", invalid)
		}
	}
	output, err := client.ProjectCommit(context.Background(), sha)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Partial bool `json:"file_list_may_be_truncated"`
		Commit  struct {
			Files []struct {
				Patch     string `json:"patch"`
				Truncated bool   `json:"patch_truncated"`
			} `json:"files"`
		} `json:"commit"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Commit.Files) != 5 {
		t.Fatal("file limit not reported")
	}
	for _, file := range result.Commit.Files {
		if !file.Truncated || len(file.Patch) > 1500 || !utf8.ValidString(file.Patch) {
			t.Fatal("patch bound lost UTF-8 or truncation flag")
		}
	}
	client.apiBase = server.URL + "/escaped"
	escaped, err := client.ProjectCommit(context.Background(), sha)
	if err != nil || len(escaped) > 10<<10 || !json.Valid([]byte(escaped)) || !strings.Contains(escaped, `"patch_truncated":true`) {
		t.Fatalf("escaped patches broke the tool budget: %v", err)
	}
}
