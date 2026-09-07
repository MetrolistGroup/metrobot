package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatCompletionClientRetriesTransportFailureOnce(t *testing.T) {
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
			if requests == 1 {
				return nil, errors.New("TLS handshake timeout")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)),
			}, nil
		})},
	)
	client.rateLimitDelay = 0

	answer, err := client.Ask(context.Background(), testGarminMessages("hi"))
	if err != nil || answer != "ok" || requests != 2 {
		t.Fatalf("Ask() = %q, %v after %d requests", answer, err, requests)
	}
}

func TestChatCompletionClientDoesNotRetryReadTimeout(t *testing.T) {
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

	client := newChatCompletionClient([]string{"key"}, server.URL, "model", "test provider", nil, nil, server.Client())
	tool := GarminAITool{Type: "function", Function: GarminAIFunctionDefinition{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	completion, err := client.Complete(context.Background(), GarminAIRequest{Messages: testGarminMessages("hi"), Tools: []GarminAITool{tool}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.Message.ToolCalls) != 1 || completion.Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("completion = %#v", completion)
	}
	messages := append(testGarminMessages("hi"), completion.Message, GarminAIMessage{Role: "tool", ToolCallID: "call-1", Content: `{"result":"ok"}`})
	final, err := client.Complete(context.Background(), GarminAIRequest{Messages: messages, Tools: []GarminAITool{tool}})
	if err != nil || final.Message.Content != "done" || requests != 2 {
		t.Fatalf("second Complete() = %#v after %d requests, error %v", final, requests, err)
	}
}

func TestGarminAIMessageMarshalsVisionContent(t *testing.T) {
	message := GarminAIMessage{Role: "user", Name: "discord_123456789012345678", Content: "what is this?", Images: []string{"https://cdn.discordapp.com/image.png"}}
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
		t.Fatalf("message = %#v", payload)
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
	_, err := client.Complete(context.Background(), GarminAIRequest{SystemPrompt: "stable prompt", Context: "dynamic context", Messages: testGarminMessages("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 3 || !request.Messages[0].Cache || request.Messages[1].Cache || request.Messages[1].Content != "dynamic context" {
		t.Fatalf("messages = %#v", request.Messages)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type timeoutReader struct{}

func (timeoutReader) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }

func testGarminMessages(prompt string) []GarminAIMessage {
	return []GarminAIMessage{{Role: "user", Content: prompt}}
}
