package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestDeepSeekClientRotatesKeys(t *testing.T) {
	var mu sync.Mutex
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := newDeepSeekClient([]string{"key-one", "key-two"}, server.URL, server.Client())
	for range 3 {
		if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err != nil {
			t.Fatalf("Ask() error = %v", err)
		}
	}

	want := []string{"Bearer key-one", "Bearer key-two", "Bearer key-one"}
	if !reflect.DeepEqual(authorizations, want) {
		t.Fatalf("authorizations = %v, want %v", authorizations, want)
	}
}

func TestDeepSeekClientFailsOverToNextKey(t *testing.T) {
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if len(authorizations) == 1 {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"insufficient balance"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"fallback"}}]}`))
	}))
	defer server.Close()

	client := newDeepSeekClient([]string{"empty", "funded"}, server.URL, server.Client())
	answer, err := client.Ask(context.Background(), testGarminMessages("hi"))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer != "fallback" {
		t.Fatalf("Ask() = %q, want %q", answer, "fallback")
	}
	want := []string{"Bearer empty", "Bearer funded"}
	if !reflect.DeepEqual(authorizations, want) {
		t.Fatalf("authorizations = %v, want %v", authorizations, want)
	}
}

func TestDeepSeekClientFailsOverAfterNonJSONServerError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("temporarily unavailable"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"fallback"}}]}`))
	}))
	defer server.Close()

	client := newDeepSeekClient([]string{"key-one", "key-two"}, server.URL, server.Client())
	answer, err := client.Ask(context.Background(), testGarminMessages("hi"))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer != "fallback" || requests != 2 {
		t.Fatalf("Ask() = %q after %d requests, want fallback after 2", answer, requests)
	}
}

func TestDeepSeekClientSendsBoundedNonThinkingRequest(t *testing.T) {
	var request chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	client := newDeepSeekClient([]string{"key"}, server.URL, server.Client())
	messages := []GarminAIMessage{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "  explain this  "},
	}
	if _, err := client.Ask(context.Background(), messages); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	if request.Model != deepSeekModel {
		t.Errorf("model = %q, want %q", request.Model, deepSeekModel)
	}
	if request.Thinking.Type != "disabled" {
		t.Errorf("thinking type = %q, want disabled", request.Thinking.Type)
	}
	if request.MaxTokens != 150 {
		t.Errorf("max tokens = %d, want 150", request.MaxTokens)
	}
	wantMessages := []chatMessage{
		{Role: "system", Content: garminSystemPrompt},
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "explain this"},
	}
	if !reflect.DeepEqual(request.Messages, wantMessages) {
		t.Errorf("messages = %#v", request.Messages)
	}
}

func TestDeepSeekClientRequiresKey(t *testing.T) {
	client := NewDeepSeekClient(nil)
	if _, err := client.Ask(context.Background(), testGarminMessages("hi")); err == nil {
		t.Fatal("Ask() error = nil, want missing-key error")
	}
}

func testGarminMessages(prompt string) []GarminAIMessage {
	return []GarminAIMessage{{Role: "user", Content: prompt}}
}
