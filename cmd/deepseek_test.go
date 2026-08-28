package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestDeepSeekClientSendsBoundedLowReasoningRequest(t *testing.T) {
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
		{Role: "user", Name: "discord_123456789012345678", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "  explain this  "},
	}
	if _, err := client.Ask(context.Background(), messages); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	if request.Model != deepSeekModel {
		t.Errorf("model = %q, want %q", request.Model, deepSeekModel)
	}
	if request.Thinking.Type != "enabled" || request.ReasoningEffort != "low" {
		t.Errorf("thinking = %#v, effort = %q, want enabled low", request.Thinking, request.ReasoningEffort)
	}
	if request.MaxTokens != 160 {
		t.Errorf("max tokens = %d, want 160", request.MaxTokens)
	}
	wantMessages := []chatMessage{
		{Role: "system", Content: garminSystemPrompt + "\n\nRuntime model identity:\n- The exact API model powering this response is `" + deepSeekModel + "`.\n- If asked what model you are, state this exact model ID. You are still Metrobot, the Discord bot; do not claim to be a different model or provider."},
		{Role: "user", Name: "discord_123456789012345678", Content: "first question"},
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

func TestChatCompletionClientDoesNotRetryReadTimeoutOnSameModel(t *testing.T) {
	requests := 0
	client := newChatCompletionClient(
		[]string{"key"},
		"https://example.com",
		"model",
		"test provider",
		nil,
		nil,
		&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(timeoutReader{})}, nil
		})},
	)

	_, err := client.Ask(context.Background(), testGarminMessages("hi"))
	if err == nil || requests != 1 {
		t.Fatalf("Ask() error = %v after %d requests", err, requests)
	}
}

func TestChatCompletionClientRoundTripsToolCalls(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if len(request.Tools) != 1 || request.ToolChoice != "auto" {
			t.Errorf("tools = %#v, choice = %q", request.Tools, request.ToolChoice)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning_content":"need the lookup","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`))
			return
		}
		if len(request.Messages) < 2 || request.Messages[len(request.Messages)-2].ReasoningContent != "need the lookup" {
			t.Errorf("tool turn lost reasoning content: %#v", request.Messages)
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`))
	}))
	defer server.Close()

	client := newDeepSeekClient([]string{"key"}, server.URL, server.Client())
	completion, err := client.Complete(context.Background(), GarminAIRequest{
		Messages: testGarminMessages("hi"),
		Tools: []GarminAITool{{
			Type: "function",
			Function: GarminAIFunctionDefinition{
				Name:       "lookup",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if requests != 1 || completion.Message.ReasoningContent != "need the lookup" || len(completion.Message.ToolCalls) != 1 || completion.Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("Complete() = %#v after %d requests", completion, requests)
	}
	messages := append(testGarminMessages("hi"), completion.Message, GarminAIMessage{Role: "tool", ToolCallID: "call-1", Content: `{"result":"ok"}`})
	final, err := client.Complete(context.Background(), GarminAIRequest{Messages: messages, Tools: []GarminAITool{{
		Type: "function", Function: GarminAIFunctionDefinition{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)},
	}}})
	if err != nil || final.Message.Content != "done" || requests != 2 {
		t.Fatalf("second Complete() = %#v after %d requests, error %v", final, requests, err)
	}
}

func TestGarminAIMessageMarshalsVisionContent(t *testing.T) {
	message := GarminAIMessage{
		Role:    "user",
		Name:    "discord_123456789012345678",
		Content: "what is this?",
		Images:  []string{"https://cdn.discordapp.com/image.png"},
	}
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	parts, ok := payload["content"].([]any)
	if !ok || len(parts) != 2 || payload["name"] != "discord_123456789012345678" {
		t.Fatalf("named vision message = %#v", payload)
	}
	imagePart, ok := parts[1].(map[string]any)
	imageURL, okURL := imagePart["image_url"].(map[string]any)
	if !ok || !okURL || imagePart["type"] != "image_url" || imageURL["url"] != "https://cdn.discordapp.com/image.png" {
		t.Fatalf("image part = %#v", parts[1])
	}
}

func TestChatCompletionClientSeparatesDynamicContextFromCachedPrompt(t *testing.T) {
	var request chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	client := newOpenRouterClient([]string{"key"}, "", server.URL, server.Client())
	_, err := client.Complete(context.Background(), GarminAIRequest{
		SystemPrompt: "stable prompt",
		Context:      "dynamic context",
		Messages:     testGarminMessages("hi"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 3 || !request.Messages[0].Cache || request.Messages[1].Cache || request.Messages[1].Content != "dynamic context" {
		t.Fatalf("messages = %#v", request.Messages)
	}
}

func TestFallbackGarminAISharesDeadlineAcrossProviders(t *testing.T) {
	first := &scriptedGarminAI{complete: func(ctx context.Context, _ GarminAIRequest) (*GarminAICompletion, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	second := &scriptedGarminAI{complete: func(context.Context, GarminAIRequest) (*GarminAICompletion, error) {
		return &GarminAICompletion{Message: GarminAIMessage{Content: "fallback"}}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	completion, err := NewFallbackGarminAI(first, second).Complete(ctx, GarminAIRequest{Messages: testGarminMessages("hi")})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completion.Message.Content != "fallback" || first.calls != 1 || second.calls != 1 {
		t.Fatalf("Complete() = %#v, calls = (%d, %d)", completion, first.calls, second.calls)
	}
}

func TestFallbackGarminAIReturnsAllProviderErrors(t *testing.T) {
	first := &scriptedGarminAI{complete: func(context.Context, GarminAIRequest) (*GarminAICompletion, error) {
		return nil, errors.New("first failed")
	}}
	second := &scriptedGarminAI{complete: func(context.Context, GarminAIRequest) (*GarminAICompletion, error) {
		return nil, errors.New("second failed")
	}}
	_, err := NewFallbackGarminAI(first, second).Complete(context.Background(), GarminAIRequest{Messages: testGarminMessages("hi")})
	if err == nil || !strings.Contains(err.Error(), "first failed") || !strings.Contains(err.Error(), "second failed") {
		t.Fatalf("Complete() error = %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type timeoutReader struct{}

func (timeoutReader) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }

type scriptedGarminAI struct {
	calls    int
	complete func(context.Context, GarminAIRequest) (*GarminAICompletion, error)
}

func (s *scriptedGarminAI) Complete(ctx context.Context, request GarminAIRequest) (*GarminAICompletion, error) {
	s.calls++
	return s.complete(ctx, request)
}

func testGarminMessages(prompt string) []GarminAIMessage {
	return []GarminAIMessage{{Role: "user", Content: prompt}}
}
