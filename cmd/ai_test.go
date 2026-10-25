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
	"testing"
	"time"
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
		&http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(timeoutReader{ctx: r.Context()})}, nil
		})},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := client.Ask(ctx, testGarminMessages("hi"))
	if !errors.Is(err, context.DeadlineExceeded) || requests != 1 {
		t.Fatalf("Ask() error = %v after %d requests", err, requests)
	}
}

func TestChatCompletionClientRetriesInterruptedResponseBody(t *testing.T) {
	for _, keys := range [][]string{{"one"}, {"one", "two"}} {
		var authorizations []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			if len(authorizations) == 1 {
				w.Header().Set("Content-Length", "1000")
				_, _ = io.WriteString(w, `{"choices":[`)
				return
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
		}))
		client := newChatCompletionClient(keys, server.URL, "model", "test provider", nil, nil, server.Client())
		client.rateLimitDelay = 0
		answer, err := client.Ask(context.Background(), testGarminMessages("hi"))
		server.Close()
		want := []string{"Bearer one", "Bearer " + keys[1%len(keys)]}
		if err != nil || answer != "ok" || !reflect.DeepEqual(authorizations, want) {
			t.Fatalf("interrupted body did not retry: answer=%q error=%v authorizations=%v", answer, err, authorizations)
		}
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
		if len(request.Tools) != 1 || request.ToolChoice != "auto" || !request.Tools[0].Function.Strict || string(request.Tools[0].Function.Parameters) != `{"type":"object","additionalProperties":false}` {
			t.Errorf("tools = %#v, choice = %q", request.Tools, request.ToolChoice)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning":"lookup rationale","reasoning_content":"need the lookup","reasoning_details":[{"type":"reasoning.encrypted","data":"opaque","signature":"signed"}],"tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`))
			return
		}
		if len(request.Messages) != 4 {
			t.Errorf("messages = %#v", request.Messages)
		} else {
			assistant, toolResult := request.Messages[2], request.Messages[3]
			if assistant.Reasoning != "lookup rationale" || assistant.ReasoningContent != "need the lookup" || string(assistant.ReasoningDetails) != `[{"type":"reasoning.encrypted","data":"opaque","signature":"signed"}]` || !reflect.DeepEqual(assistant.ToolCalls, []GarminAIToolCall{{ID: "call-1", Type: "function", Function: GarminAIFunctionCall{Name: "lookup", Arguments: "{}"}}}) {
				t.Errorf("tool turn lost reasoning or call identity: %#v", assistant)
			}
			if toolResult.Role != "tool" || toolResult.ToolCallID != "call-1" || toolResult.Content != "  {\"result\":\"ok\"}\n" {
				t.Errorf("tool result changed: %#v", toolResult)
			}
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":" done\n"}}]}`))
	}))
	defer server.Close()

	client := newChatCompletionClient([]string{"key"}, server.URL, "model", "test provider", nil, nil, server.Client())
	tool := GarminAITool{Type: "function", Function: GarminAIFunctionDefinition{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`), Strict: true}}
	completion, err := client.Complete(context.Background(), GarminAIRequest{Messages: testGarminMessages("hi"), Tools: []GarminAITool{tool}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.Message.ToolCalls) != 1 || completion.Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("completion = %#v", completion)
	}
	messages := append(testGarminMessages("hi"), completion.Message, GarminAIMessage{Role: "tool", ToolCallID: "call-1", Content: "  {\"result\":\"ok\"}\n"})
	final, err := client.Complete(context.Background(), GarminAIRequest{Messages: messages, Tools: []GarminAITool{tool}})
	if err != nil || final.Message.Content != " done\n" || requests != 2 {
		t.Fatalf("second Complete() = %#v after %d requests, error %v", final, requests, err)
	}
}

func TestChatCompletionClientAllowsConcurrentRequestsAndRotatesKeys(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.Header.Get("Authorization")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()
	defer close(release)

	client := newChatCompletionClient([]string{"first", "second"}, server.URL, "model", "test provider", nil, nil, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := client.Ask(ctx, testGarminMessages("hi"))
			errs <- err
		}()
	}
	keys := make(map[string]bool)
	for range 2 {
		select {
		case key := <-entered:
			keys[key] = true
		case <-ctx.Done():
			t.Fatal("second request waited for the first provider response")
		}
	}
	if !keys["Bearer first"] || !keys["Bearer second"] {
		t.Fatalf("concurrent key rotation = %v", keys)
	}
	// Both requests have reached the provider before either response is released.
	release <- struct{}{}
	release <- struct{}{}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestGarminAIMessageMarshalsVisionContent(t *testing.T) {
	message := GarminAIMessage{Role: "user", Name: "discord_123456789012345678", Content: "what is this?", Images: []string{"https://cdn.discordapp.com/image.png"}, Timestamp: time.Now().UTC()}
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["Timestamp"]; ok {
		t.Fatal("local timestamp leaked into provider message protocol")
	}
	if err := json.Unmarshal(data, &message); err != nil || !message.Timestamp.IsZero() {
		t.Fatalf("provider message retained a local timestamp: %#v, %v", message, err)
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

	client := newOpenRouterClient([]string{"key"}, "qwen/qwen3-coder-flash", server.URL, server.Client())
	var prefix string
	for _, dynamic := range []string{"first private memory", "changed memory and skill"} {
		_, err := client.Complete(context.Background(), GarminAIRequest{SystemPrompt: garminSystemPrompt + "\n\n" + dynamic, Context: "dynamic profile", Messages: testGarminMessages("hi")})
		if err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) != 4 || !request.Messages[0].Cache || request.Messages[1].Cache || request.Messages[2].Cache || request.Messages[3].Cache || request.Messages[1].Content != "\n\n"+dynamic || request.Messages[2].Content != "dynamic profile" {
			t.Fatalf("messages = %#v", request.Messages)
		}
		if prefix != "" && request.Messages[0].Content != prefix {
			t.Fatal("volatile data changed the cached prefix")
		}
		prefix = request.Messages[0].Content
	}
}

func TestGarminAIMessageCacheControlWireContract(t *testing.T) {
	for _, cache := range []bool{false, true} {
		data, err := json.Marshal(GarminAIMessage{Role: "system", Content: "stable", Cache: cache})
		if err != nil {
			t.Fatal(err)
		}
		want := `{"role":"system","content":"stable"}`
		if cache {
			want = `{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]}`
		}
		if string(data) != want {
			t.Fatalf("cache=%v: %s, want %s", cache, data, want)
		}
	}
	original := GarminAIMessage{ReasoningDetails: json.RawMessage(`[{"data":"original"}]`)}
	copied := original
	if err := json.Unmarshal([]byte(`{"role":"assistant","reasoning_details":[{"data":"replaced"}]}`), &copied); err != nil {
		t.Fatal(err)
	}
	if string(original.ReasoningDetails) != `[{"data":"original"}]` {
		t.Fatal("decoding mutated reasoning shared with another message")
	}
}

func TestChatCompletionClientPreservesSharedInput(t *testing.T) {
	input := GarminAIRequest{
		Messages: []GarminAIMessage{{Role: "assistant", Content: "  exact text\n", Cache: true, Images: []string{"https://example.com/image"}, ToolCalls: []GarminAIToolCall{{ID: "call"}}, ReasoningDetails: json.RawMessage(`[{"data":"original"}]`)}},
		Tools:    []GarminAITool{{Function: GarminAIFunctionDefinition{Parameters: json.RawMessage(`{"type":"object"}`)}}},
	}
	before, _ := json.Marshal(input)
	client := newChatCompletionClient([]string{"one", "two"}, "https://example.com", "model", "test", nil, nil, &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))}, nil
	})})
	// Mutate the local snapshots before marshaling, while keeping their JSON valid.
	client.configureRequest = func(request *chatCompletionRequest) {
		message := &request.Messages[1]
		if message.Content != input.Messages[0].Content || !message.Cache {
			t.Error("caller content/cache flag changed")
		}
		message.Images[0] = "replaced"
		message.ToolCalls[0].ID = "replaced"
		copy(message.ReasoningDetails, `[{"data":"replaced"}]`)
		copy(request.Tools[0].Function.Parameters, `{"type":"string"}`)
	}
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := client.Complete(context.Background(), input)
			errs <- err
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("concurrent requests mutated caller-owned slices")
	}
}

func TestChatCompletionClientTriesUntouchedKeysBeforeRetrying(t *testing.T) {
	for _, transport := range []bool{false, true} {
		var authorizations []string
		client := newChatCompletionClient([]string{"1", "2", "3", "4", "5"}, "https://example.com", "model", "test", nil, nil, &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			if r.Header.Get("Authorization") == "Bearer 5" {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))}, nil
			}
			if transport {
				return nil, errors.New("transport failed")
			}
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		answer, err := client.Ask(ctx, testGarminMessages("hi"))
		cancel()
		want := []string{"Bearer 1", "Bearer 2", "Bearer 3", "Bearer 4", "Bearer 5"}
		if err != nil || answer != "ok" || !reflect.DeepEqual(authorizations, want) {
			t.Fatalf("transport=%v: answer=%q error=%v authorizations=%v", transport, answer, err, authorizations)
		}
	}
}

func TestChatCompletionClientRetriesAvailableKeyBeforeThrottledKey(t *testing.T) {
	var authorizations []string
	client := newChatCompletionClient([]string{"limited", "available"}, "https://example.com", "model", "test", nil, nil, &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer limited" {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		if len(authorizations) == 2 {
			return nil, errors.New("transient transport failure")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))}, nil
	})})
	client.rateLimitDelay = 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	answer, err := client.Ask(ctx, testGarminMessages("hi"))
	want := []string{"Bearer limited", "Bearer available", "Bearer available"}
	if err != nil || answer != "ok" || !reflect.DeepEqual(authorizations, want) {
		t.Fatalf("answer=%q error=%v authorizations=%v", answer, err, authorizations)
	}
}

func TestChatCompletionClientRetryBudgetsWithManyKeys(t *testing.T) {
	for _, keyCount := range []int{1, 2, 6} {
		for _, transport := range []bool{false, true} {
			calls := 0
			keys := []string{"1", "2", "3", "4", "5", "6"}
			client := newChatCompletionClient(keys[:keyCount], "https://example.com", "model", "test", nil, nil, &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if transport {
					return nil, errors.New("private transport error")
				}
				return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private error"))}, nil
			})})
			client.rateLimitDelay = 0
			_, err := client.Ask(context.Background(), testGarminMessages("hi"))
			want := max(keyCount, chatCompletionRateLimitRetries+1)
			if transport {
				want = max(keyCount, chatCompletionTransportRetries+1)
			}
			if err == nil || calls != want || strings.Contains(err.Error(), "private") {
				t.Fatalf("keys=%d transport=%v: calls=%d want %d, error=%v", keyCount, transport, calls, want, err)
			}
		}
	}
}

func TestWaitForChatCompletionRetryWithinDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForChatCompletionRetry(ctx, time.Millisecond); err != nil {
		t.Fatalf("usable retry delay rejected: %v", err)
	}
	cancel()
	if err := waitForChatCompletionRetry(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation replaced by insufficient-budget error: %v", err)
	}
}

func TestChatCompletionClientRejectsBackoffBeyondDeadline(t *testing.T) {
	calls := 0
	client := newChatCompletionClient([]string{"one", "two"}, "https://example.com", "model", "test", nil, nil, &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.Ask(ctx, testGarminMessages("hi"))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) || calls != 2 || ctx.Err() != nil {
			t.Fatalf("backoff did not fail promptly: error=%v calls=%d context=%v", err, calls, ctx.Err())
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("unusable backoff held the request open")
	}
}

func TestChatCompletionClientCancellationAndRetryAfter(t *testing.T) {
	for _, status := range []int{200, 429} {
		calls := 0
		client := newChatCompletionClient([]string{"one"}, "https://example.com", "model", "test", nil, nil, &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if status == 200 {
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})})
		client.rateLimitDelay = 0
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		start := time.Now()
		_, err := client.Ask(ctx, testGarminMessages("hi"))
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || calls != 1 || time.Since(start) > time.Second {
			t.Fatalf("status=%d: deadline error=%v calls=%d", status, err, calls)
		}
		ctx, cancel = context.WithCancel(context.Background())
		cancel()
		if _, err := client.Ask(ctx, testGarminMessages("hi")); !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("already canceled request sent/retried: %v calls=%d", err, calls)
		}
	}
	if chatCompletionRetryAfter("2") != 2*time.Second || chatCompletionRetryAfter("invalid") != 0 || chatCompletionRetryAfter("-1") != 0 || chatCompletionRetryAfter(time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)) < 58*time.Second {
		t.Fatal("invalid Retry-After parsing")
	}
}

func TestChatCompletionClientRejectsMalformedAndOversizedResponses(t *testing.T) {
	valid := `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
	for _, body := range []string{
		valid + strings.Repeat(" ", chatCompletionResponseMax),
		valid + ` {}`,
		`{"error":{"message":"private-key private input"}}`,
		`{"error":{"message":"private input"},"choices":[{"message":{"content":"unsafe"}}]}`,
		`{"choices":[{"message":{"role":"user","content":"wrong speaker"}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"","reasoning_details":null}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"","reasoning_details":[]}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"","reasoning_details":{}}}]}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"null"}}]}}]}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"[1]"}}]}}]}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}},{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`,
	} {
		calls := 0
		client := newChatCompletionClient([]string{"one", "two"}, "https://example.com", "model", "test", nil, nil, &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})})
		if result, err := client.Complete(context.Background(), GarminAIRequest{Messages: testGarminMessages("hi")}); err == nil || result != nil || calls != 1 || strings.Contains(err.Error(), "private") {
			t.Fatalf("malformed response accepted/retried: result=%#v error=%v calls=%d", result, err, calls)
		}
	}
	client := newChatCompletionClient([]string{"key"}, ":bad", "model", "test", nil, nil, http.DefaultClient)
	if _, err := client.Ask(context.Background(), testGarminMessages(strings.Repeat("x", chatCompletionRequestMax))); err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatalf("oversized request accepted: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type timeoutReader struct{ ctx context.Context }

func (r timeoutReader) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func testGarminMessages(prompt string) []GarminAIMessage {
	return []GarminAIMessage{{Role: "user", Content: prompt}}
}
