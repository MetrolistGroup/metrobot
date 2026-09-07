package firecrawl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestClientRotatesKeysAndFailsOver(t *testing.T) {
	var authorizations []string
	var requests []searchRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		var request searchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") == "Bearer limited" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"success":false,"error":"rate limited"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"web":[{"url":"https://example.com","markdown":"result"}]}}`))
	}))
	defer server.Close()

	client := newClient([]string{"limited", "available"}, server.URL, server.Client())
	for range 3 {
		result, err := client.Search(context.Background(), "latest Android news", 0, "web")
		if err != nil || !json.Valid([]byte(result)) {
			t.Fatalf("Search() = %q, %v", result, err)
		}
	}

	if want := []string{"Bearer limited", "Bearer available", "Bearer available", "Bearer available"}; !reflect.DeepEqual(authorizations, want) {
		t.Fatalf("authorizations = %v, want %v", authorizations, want)
	}
	if remaining := time.Until(time.Unix(client.failedUntil[0].Load(), 0)); remaining < 47*time.Hour {
		t.Fatalf("failed key cooldown = %s, want about 48 hours", remaining)
	}
	for _, request := range requests {
		if request.Query != "latest Android news" || request.Limit != 3 || request.ScrapeOptions == nil || !reflect.DeepEqual(request.ScrapeOptions.Formats, []string{"markdown"}) {
			t.Fatalf("request = %#v", request)
		}
	}
}

func TestClientSkipsAllCoolingKeys(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client := newClient([]string{"one", "two"}, server.URL, server.Client())
	for index := range client.failedUntil {
		client.failedUntil[index].Store(time.Now().Add(failedKeyCooldown).Unix())
	}

	_, err := client.Search(context.Background(), "query", 1, "web")
	if err == nil || err.Error() != "all Firecrawl API keys are cooling down" || requests != 0 {
		t.Fatalf("Search() error = %v after %d requests", err, requests)
	}
}

func TestClientRequestsImageResultsWithoutScraping(t *testing.T) {
	var request searchRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"images":[{"imageUrl":"https://example.com/moai.jpg"}]}}`))
	}))
	defer server.Close()

	client := newClient([]string{"key"}, server.URL, server.Client())
	if _, err := client.Search(context.Background(), "moai sunglasses meme", 1, "images"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Sources, []string{"images"}) || request.ScrapeOptions != nil {
		t.Fatalf("request = %#v", request)
	}
}
