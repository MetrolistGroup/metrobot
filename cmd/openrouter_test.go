package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenRouterClientUsesCapableRouteByDefault(t *testing.T) {
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

	if request.Model != openRouterDefaultModel || len(request.Models) != 0 {
		t.Errorf("model route = (%q, %v), want %q", request.Model, request.Models, openRouterDefaultModel)
	}
	if request.Reasoning == nil || request.Reasoning.Enabled == nil || *request.Reasoning.Enabled || request.Reasoning.MaxTokens != 0 || request.Reasoning.Exclude || request.MaxTokens != 1024 {
		t.Errorf("reasoning = %#v with %d output tokens, want disabled reasoning and 1024 output", request.Reasoning, request.MaxTokens)
	}
	if request.SessionID != "" {
		t.Errorf("unrelated conversations were grouped into session %q", request.SessionID)
	}
	if len(request.Messages) == 0 || request.Messages[0].Cache {
		t.Errorf("undocumented model cache support was assumed: %#v", request.Messages)
	}
	if request.Provider == nil || request.Provider.ZDR || request.Provider.DataCollection != "deny" || !request.Provider.RequireParameters || request.Provider.Sort.By != "latency" || request.Provider.Sort.Partition != "none" {
		t.Errorf("provider routing = %#v", request.Provider)
	}
	if referer != "https://github.com/MetrolistGroup/metrobot" || title != "Metrobot" {
		t.Errorf("OpenRouter attribution headers = (%q, %q)", referer, title)
	}
}

func TestOpenRouterClientPreservesReasoningOnlyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","reasoning":"drafting a greeting"},"finish_reason":"length"}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, "", server.URL, server.Client())
	completion, err := client.Complete(context.Background(), GarminAIRequest{Messages: testGarminMessages("hey!")})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completion.Message.Content != "" || completion.Message.Reasoning != "drafting a greeting" {
		t.Fatalf("reasoning-only completion = %#v", completion)
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
	if request.Model != openRouterDefaultModel || len(request.Models) != 0 {
		t.Fatalf("legacy model route = (%q, %v)", request.Model, request.Models)
	}
}

func TestOpenRouterClientMigratesBrokenGraniteDefault(t *testing.T) {
	var request chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, openRouterBrokenDefault, server.URL, server.Client())
	if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if request.Model != openRouterDefaultModel || request.Provider == nil || request.Provider.ZDR || request.Provider.DataCollection != "deny" {
		t.Fatalf("broken default migration = model %q, provider %#v", request.Model, request.Provider)
	}
}

func TestOpenRouterClientMigratesPreviousQwenDefault(t *testing.T) {
	var request chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, openRouterPreviousQwen, server.URL, server.Client())
	if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if request.Model != openRouterDefaultModel || request.Reasoning == nil || request.Reasoning.Enabled == nil || *request.Reasoning.Enabled || request.Reasoning.MaxTokens != 0 || request.Reasoning.Exclude {
		t.Fatalf("previous default migration = model %q, reasoning %#v", request.Model, request.Reasoning)
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

	client := newOpenRouterClient([]string{"key"}, "openai/gpt-4.1-mini", server.URL, server.Client())
	if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if request.Model != "openai/gpt-4.1-mini" || len(request.Models) != 0 {
		t.Errorf("model route = (%q, %v), want configured model", request.Model, request.Models)
	}
	if request.Reasoning != nil {
		t.Errorf("reasoning = %#v, want model default", request.Reasoning)
	}
	if request.Provider == nil || request.Provider.DataCollection != "deny" || !request.Provider.RequireParameters || request.Provider.Sort.By != "latency" {
		t.Errorf("configured model lost privacy/tool requirements: %#v", request.Provider)
	}
	if request.SessionID != "" || request.Messages[0].Cache {
		t.Error("configured model inherited an undocumented session/cache policy")
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
	client.rateLimitDelay = 0
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

func TestOpenRouterClientCacheCapabilitiesAndCustomPrompts(t *testing.T) {
	for _, test := range []struct {
		model, prompt string
		cache         bool
	}{
		{model: "anthropic/claude-sonnet-4", cache: true},
		{model: "qwen/qwen3-coder-flash", cache: true},
		{model: "qwen/qwen3.5-flash-02-23"},
		{model: openRouterDefaultModel},
		{model: "openai/gpt-4.1-mini"},
		{model: "vendor/unknown"},
		{model: "anthropic/claude-sonnet-4", prompt: "private custom instructions"},
	} {
		t.Run(test.model+"/"+test.prompt, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chatCompletionRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.Messages[0].Cache != test.cache || request.Messages[1].Cache || request.SessionID != "" {
					t.Errorf("unexpected automatic caching/session: %#v", request)
				}
				// Explicit caller breakpoints are preserved, never spread to neighboring messages.
				if !request.Messages[2].Cache || request.Messages[2].Content != " caller prefix " {
					t.Errorf("caller breakpoint lost: %#v", request.Messages)
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			}))
			defer server.Close()
			client := newOpenRouterClient([]string{"key"}, test.model, server.URL, server.Client())
			_, err := client.Complete(context.Background(), GarminAIRequest{SystemPrompt: test.prompt, Context: "private profile", Messages: []GarminAIMessage{{Role: "user", Content: " caller prefix ", Cache: true}}})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenRouterClientHonorsDisableReasoningForCustomModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Reasoning == nil || request.Reasoning.Enabled == nil || *request.Reasoning.Enabled {
			t.Error("explicit reasoning disable was ignored")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()
	client := newOpenRouterClient([]string{"key"}, "openai/gpt-4.1-mini", server.URL, server.Client())
	if _, err := client.Complete(context.Background(), GarminAIRequest{DisableReasoning: true, Messages: testGarminMessages("hi")}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRouterClientDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := NewOpenRouterClient([]string{"key"}, "")
	client.endpoint = server.URL
	if _, err := client.Ask(context.Background(), testGarminMessages("private conversation")); err == nil || leaked.Load() {
		t.Fatalf("redirect followed: %v", err)
	}
}

func TestOpenRouterClientRetriesRateLimitThreeTimes(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, "", server.URL, server.Client())
	client.rateLimitDelay = time.Millisecond
	_, err := client.Ask(context.Background(), testGarminMessages("hi"))
	if err == nil || requests != 4 {
		t.Fatalf("Ask() error = %v after %d requests, want initial request plus 3 retries", err, requests)
	}
}
