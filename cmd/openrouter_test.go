package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOpenRouterClientUsesSolarPro4ByDefault(t *testing.T) {
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

	if request.Model != openRouterDefaultModel {
		t.Errorf("model = %q, want %q", request.Model, openRouterDefaultModel)
	}
	if request.Reasoning == nil || request.Reasoning.Enabled {
		t.Errorf("reasoning = %#v, want disabled", request.Reasoning)
	}
	if request.Thinking != nil {
		t.Errorf("thinking = %#v, want omitted", request.Thinking)
	}
	if referer != "https://github.com/MetrolistGroup/metrobot" || title != "Metrobot" {
		t.Errorf("OpenRouter attribution headers = (%q, %q)", referer, title)
	}
}

func TestOpenRouterClientSupportsConfiguredModel(t *testing.T) {
	var model string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		model = request.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, "openai/gpt-5-mini", server.URL, server.Client())
	if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if model != "openai/gpt-5-mini" {
		t.Errorf("model = %q, want configured model", model)
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
