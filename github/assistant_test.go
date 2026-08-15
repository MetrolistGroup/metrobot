package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssistantClientRetriesPublicDataWithoutForbiddenToken(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"token forbidden"}`))
			return
		}
		if r.URL.Path == "/users/nyx" {
			_, _ = w.Write([]byte(`{"login":"nyx","name":"Nyx","html_url":"https://github.com/nyx"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewAssistantClient("bad-token", "owner", "repo")
	client.apiBase = server.URL
	client.httpClient = server.Client()
	result, err := client.User(context.Background(), "nyx")
	if err != nil {
		t.Fatalf("User() error = %v", err)
	}
	if requests != 2 || !strings.Contains(result, `"login":"nyx"`) {
		t.Fatalf("User() = %q after %d requests", result, requests)
	}
}

func TestAssistantClientSearchIssuesScopesRepository(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`{"total_count":0,"items":[]}`))
	}))
	defer server.Close()

	client := NewAssistantClient("", "MetrolistGroup", "Metrolist")
	client.apiBase = server.URL
	client.httpClient = server.Client()
	if _, err := client.SearchIssues(context.Background(), "lyrics is:open"); err != nil {
		t.Fatalf("SearchIssues() error = %v", err)
	}
	if query != "repo:MetrolistGroup/Metrolist is:issue lyrics is:open" {
		t.Fatalf("query = %q", query)
	}
}

func TestAssistantClientSearchesAndGetsPublicRepositories(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer read-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/search/repositories":
			if query := r.URL.Query().Get("q"); query != "youtube music language:kotlin is:public" {
				t.Errorf("search query = %q", query)
			}
			_, _ = w.Write([]byte(`{"total_count":1,"incomplete_results":false,"items":[{"full_name":"MetrolistGroup/Metrolist","name":"Metrolist","description":"YouTube Music client","html_url":"https://github.com/MetrolistGroup/Metrolist","language":"Kotlin","stargazers_count":100,"private":false,"owner":{"login":"MetrolistGroup"}}]}`))
		case "/repos/MetrolistGroup/Metrolist":
			_, _ = w.Write([]byte(`{"full_name":"MetrolistGroup/Metrolist","name":"Metrolist","description":"YouTube Music client","html_url":"https://github.com/MetrolistGroup/Metrolist","language":"Kotlin","default_branch":"main","private":false,"owner":{"login":"MetrolistGroup"},"license":{"spdx_id":"GPL-3.0"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewAssistantClient("read-token", "MetrolistGroup", "Metrolist")
	client.apiBase = server.URL
	client.httpClient = server.Client()
	search, err := client.SearchRepositories(context.Background(), "youtube music language:kotlin")
	if err != nil || !strings.Contains(search, `"description":"YouTube Music client"`) {
		t.Fatalf("SearchRepositories() = %q, %v", search, err)
	}
	repository, err := client.Repository(context.Background(), "https://github.com/MetrolistGroup/Metrolist")
	if err != nil || !strings.Contains(repository, `"license":"GPL-3.0"`) {
		t.Fatalf("Repository() = %q, %v", repository, err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}
