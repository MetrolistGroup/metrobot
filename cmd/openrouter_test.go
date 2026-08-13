package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOpenRouterClientUsesFastZDRRouteByDefault(t *testing.T) {
	var request chatCompletionRequest
	var referer, title string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referer = r.Header.Get("HTTP-Referer")
		title = r.Header.Get("X-OpenRouter-Title")
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, "", server.URL, server.Client())
	if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	if request.Model != "" || !reflect.DeepEqual(request.Models, openRouterDefaultModels) {
		t.Errorf("model route = (%q, %v), want %v", request.Model, request.Models, openRouterDefaultModels)
	}
	if request.Reasoning != nil {
		t.Errorf("reasoning = %#v, want omitted so all fast-route models remain eligible", request.Reasoning)
	}
	if request.Thinking != nil {
		t.Errorf("thinking = %#v, want omitted", request.Thinking)
	}
	if request.Provider == nil || !request.Provider.ZDR || request.Provider.DataCollection != "deny" || !request.Provider.RequireParameters || request.Provider.Sort.By != "throughput" || request.Provider.Sort.Partition != "none" || request.Provider.MaxPrice.Prompt != 0.10 || request.Provider.MaxPrice.Completion != 0.20 {
		t.Errorf("provider routing = %#v", request.Provider)
	}
	if referer != "https://github.com/MetrolistGroup/metrobot" || title != "Metrobot" {
		t.Errorf("OpenRouter attribution headers = (%q, %q)", referer, title)
	}
}

func TestOpenRouterClientMigratesLegacySolarDefault(t *testing.T) {
	var request chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, openRouterLegacyDefault, server.URL, server.Client())
	if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if request.Model != "" || !reflect.DeepEqual(request.Models, openRouterDefaultModels) {
		t.Fatalf("legacy model route = (%q, %v)", request.Model, request.Models)
	}
}

func TestOpenRouterClientSupportsConfiguredModel(t *testing.T) {
	var request chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, "openai/gpt-5-mini", server.URL, server.Client())
	if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if request.Model != "openai/gpt-5-mini" || len(request.Models) != 0 {
		t.Errorf("model route = (%q, %v), want configured model", request.Model, request.Models)
	}
	if request.Reasoning == nil || request.Reasoning.Enabled {
		t.Errorf("reasoning = %#v, want disabled", request.Reasoning)
	}
	if request.Provider != nil {
		t.Errorf("provider routing = %#v, want no default restrictions for configured model", request.Provider)
	}
}

func TestOpenRouterClientRotatesAndFailsOverKeys(t *testing.T) {
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if len(authorizations) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"fallback"}}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"limited", "available"}, "", server.URL, server.Client())
	answer, err := client.Ask(context.Background(), testGarminMessages("hi"))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer != "fallback" {
		t.Fatalf("Ask() = %q, want fallback", answer)
	}
	want := []string{"Bearer limited", "Bearer available"}
	if !reflect.DeepEqual(authorizations, want) {
		t.Fatalf("authorizations = %v, want %v", authorizations, want)
	}
}
