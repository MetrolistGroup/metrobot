package firecrawl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
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
	for range 2 {
		result, err := client.Search(context.Background(), "latest Android news", 0)
		if err != nil || !json.Valid([]byte(result)) {
			t.Fatalf("Search() = %q, %v", result, err)
		}
	}

	if want := []string{"Bearer limited", "Bearer available", "Bearer available"}; !reflect.DeepEqual(authorizations, want) {
		t.Fatalf("authorizations = %v, want %v", authorizations, want)
	}
	for _, request := range requests {
		if request.Query != "latest Android news" || request.Limit != 3 || !reflect.DeepEqual(request.ScrapeOptions.Formats, []string{"markdown"}) {
			t.Fatalf("request = %#v", request)
		}
	}
}
