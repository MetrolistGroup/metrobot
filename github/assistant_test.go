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
