package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const decisionTestResponse = `{"model":"perplexity/pplx-decider-flash-v1.2-20261016","answers":{"should_answer":{"type":"noul","noul":0.99},"route":{"type":"choice","choice":"n0","probabilities":{"n0":0.98,"none":0.02},"confidence":0.96}},"usage":{"input_tokens":104,"output_tokens":1,"cost":0.00000104},"id":"synthetic","provider":"Perplexity"}`

func decisionTestRequest() DecisionRequest {
	return DecisionRequest{State: map[string]string{"message": "How do I install the nightly build?"}, Questions: map[string]DecisionQuestion{
		"should_answer": {Type: "noul", Instructions: "Is this help seeking?", Criteria: map[string]string{"true": "Help seeking", "false": "Chatter"}},
		"route":         {Type: "choice", Instructions: "Choose the best support action.", Criteria: map[string]string{"n0": "How to get nightly builds", "none": "Not a support request"}},
	}}
}

func TestDecisionsWireContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/alpha/decisions" || r.Header.Get("Authorization") != "Bearer private-key" ||
			r.Header.Get("Content-Type") != "application/json" || r.Header.Get("HTTP-Referer") != "https://github.com/MetrolistGroup/metrobot" || r.Header.Get("X-OpenRouter-Title") != "Metrobot" {
			t.Errorf("invalid request method/path/headers")
		}
		var body struct {
			DecisionRequest
			Model    string          `json:"model"`
			Provider map[string]any  `json:"provider"`
			Messages json.RawMessage `json:"messages"`
			Tools    json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "perplexity/pplx-decider-flash-v1.2" || body.Provider["data_collection"] != "deny" || body.Provider["sort"] != "latency" || body.Provider["require_parameters"] != true ||
			body.Questions["should_answer"].Type != "noul" || body.Questions["route"].Type != "choice" || body.Messages != nil || body.Tools != nil {
			t.Errorf("invalid Decisions schema: %#v", body)
		}
		_, _ = io.WriteString(w, decisionTestResponse)
	}))
	defer server.Close()
	client := newDecisionsClient([]string{"private-key"}, "perplexity/pplx-decider-flash-v1.2", server.URL+"/api/alpha/decisions", server.Client())
	response, err := client.Decide(context.Background(), decisionTestRequest())
	if err != nil || response == nil || response.Answers["route"].Choice != "n0" || *response.Usage.Cost != 0.00000104 {
		t.Fatalf("decision = %#v, %v", response, err)
	}
}

func TestDecisionsRejectInvalidResponses(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"missing answer", `"should_answer":{"type":"noul","noul":0.99},`, ""},
		{"wrong type", `"type":"noul"`, `"type":"choice"`},
		{"missing noul", `,"noul":0.99`, ""},
		{"null noul", `"noul":0.99`, `"noul":null`},
		{"large noul", `"noul":0.99`, `"noul":1.01`},
		{"NaN", `"noul":0.99`, `"noul":NaN`},
		{"quoted NaN", `"noul":0.99`, `"noul":"NaN"`},
		{"overflow", `"noul":0.99`, `"noul":1e999`},
		{"unknown choice", `"choice":"n0"`, `"choice":"surprise"`},
		{"winner mismatch", `"choice":"n0"`, `"choice":"none"`},
		{"missing probability", `,"none":0.02`, ""},
		{"unknown probability", `"none":0.02`, `"surprise":0.02`},
		{"null probability", `"none":0.02`, `"none":null`},
		{"negative probability", `"none":0.02`, `"none":-0.02`},
		{"invalid sum", `"n0":0.98`, `"n0":0.5`},
		{"bad confidence", `"confidence":0.96`, `"confidence":2`},
		{"missing model", `"model":"perplexity/pplx-decider-flash-v1.2-20261016",`, ""},
		{"model containing input", `"model":"perplexity/pplx-decider-flash-v1.2-20261016"`, `"model":"private text https://private.example"`},
		{"missing usage", `"usage":{"input_tokens":104,"output_tokens":1,"cost":0.00000104},`, ""},
		{"missing token count", `"input_tokens":104,`, ""},
		{"invalid cost", `"cost":0.00000104`, `"cost":-1`},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := strings.Replace(decisionTestResponse, test.old, test.replacement, 1)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, body) }))
			defer server.Close()
			client := newDecisionsClient([]string{"key"}, "model", server.URL, server.Client())
			if response, err := client.Decide(context.Background(), decisionTestRequest()); err == nil || response != nil || calls.Load() != 1 {
				t.Fatalf("malformed response accepted/retried: response=%#v error=%v calls=%d", response, err, calls.Load())
			}
		})
	}
	var response DecisionResponse
	if err := json.Unmarshal([]byte(decisionTestResponse), &response); err != nil {
		t.Fatal(err)
	}
	p := math.NaN()
	response.Answers["route"].Probabilities["none"] = &p
	if response.ValidFor(decisionTestRequest()) {
		t.Fatal("nonfinite probability accepted")
	}
}

func TestDecisionsBodyLimitsAndRedaction(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", DecisionsBodyMax+1), `{"error":{"message":"private text private-key https://private.example"}}`, decisionTestResponse + ` {}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		client := newDecisionsClient([]string{"private-key"}, "model", server.URL, server.Client())
		_, err := client.Decide(context.Background(), decisionTestRequest())
		server.Close()
		if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "http") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	client := newDecisionsClient([]string{"key"}, "model", ":bad", http.DefaultClient)
	request := decisionTestRequest()
	request.State = strings.Repeat("x", DecisionsBodyMax)
	if _, err := client.Decide(context.Background(), request); err == nil {
		t.Fatal("oversize request accepted")
	}
}

func TestDecisionsBoundedRetryAndKeyRotation(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 408, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				want := "Bearer limited"
				if n == 2 {
					want = "Bearer available"
				}
				if r.Header.Get("Authorization") != want {
					t.Errorf("key failover failed")
				}
				if n == 1 {
					w.Header().Set("Retry-After", "60")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "private text private-key https://private.example")
					return
				}
				_, _ = io.WriteString(w, decisionTestResponse)
			}))
			defer server.Close()
			client := newDecisionsClient([]string{"limited", "available"}, "model", server.URL, server.Client())
			response, err := client.Decide(context.Background(), decisionTestRequest())
			if status == 400 {
				if err == nil || response != nil || calls.Load() != 1 || strings.Contains(err.Error(), "private") {
					t.Fatalf("HTTP failure = %v calls=%d", err, calls.Load())
				}
			} else if err != nil || response == nil || calls.Load() != 2 {
				t.Fatalf("failover = %v calls=%d", err, calls.Load())
			}
		})
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	client := newDecisionsClient([]string{"key"}, "model", server.URL, server.Client())
	if _, err := client.Decide(context.Background(), decisionTestRequest()); err == nil || calls.Load() != 2 {
		t.Fatalf("unbounded retry: %v calls=%d", err, calls.Load())
	}
}

func TestDecisionsCancellationAndDeadline(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := newDecisionsClient([]string{"key", "other"}, "model", server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := client.Decide(ctx, decisionTestRequest()); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second || calls.Load() != 1 {
		t.Fatalf("deadline = %v calls=%d", err, calls.Load())
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := client.Decide(ctx, decisionTestRequest()); !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("canceled request = %v calls=%d", err, calls.Load())
	}
}

type decisionTestTransport func(*http.Request) (*http.Response, error)

func (f decisionTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDecisionsTransportBudgetAndRedaction(t *testing.T) {
	calls := 0
	client := newDecisionsClient([]string{"key", "next"}, "model", "https://private.example", &http.Client{Transport: decisionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > decisionsAttemptTimeout {
			t.Error("attempt has no bounded deadline")
		}
		if calls == 1 {
			<-r.Context().Done()
			return nil, errors.New("private-key https://private.example user body")
		}
		if r.Header.Get("Authorization") != "Bearer next" {
			t.Error("timeout did not fail over")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(decisionTestResponse)), Header: make(http.Header)}, nil
	})})
	start := time.Now()
	if _, err := client.Decide(context.Background(), decisionTestRequest()); err != nil || calls != 2 || time.Since(start) > DecisionsTimeout {
		t.Fatalf("timeout failover = %v calls=%d", err, calls)
	}
	client.http.Transport = decisionTestTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("private-key https://private.example user body")
	})
	if _, err := client.Decide(context.Background(), decisionTestRequest()); err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "https") {
		t.Fatalf("unsafe transport error: %v", err)
	}
}

func TestDecisionsHonorsRetryAfterWithinParentDeadline(t *testing.T) {
	calls := 0
	client := newDecisionsClient([]string{"one"}, "model", "https://example.com", &http.Client{Transport: decisionTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := client.Decide(ctx, decisionTestRequest()); !errors.Is(err, context.DeadlineExceeded) || calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("retry ignored rate-limit/parent deadline: %v calls=%d", err, calls)
	}
}

func TestDecisionsRotatesInitialKey(t *testing.T) {
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, decisionTestResponse)
	}))
	defer server.Close()
	client := newDecisionsClient([]string{"one", "two"}, "model", server.URL, server.Client())
	for i := 0; i < 3; i++ {
		if _, err := client.Decide(context.Background(), decisionTestRequest()); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(keys, ",") != "Bearer one,Bearer two,Bearer one" {
		t.Fatalf("rotation=%v", keys)
	}
}

func TestDecisionsFiltersBlankKeysAndSupportsConcurrentCalls(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.Header.Get("Authorization")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, decisionTestResponse)
	}))
	defer server.Close()
	defer close(release)
	client := newDecisionsClient([]string{" ", " one ", "", "two"}, "model", server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := client.Decide(ctx, decisionTestRequest())
			errs <- err
		}()
	}
	keys := make(map[string]bool)
	for range 2 {
		select {
		case key := <-entered:
			keys[key] = true
		case <-ctx.Done():
			t.Fatal("concurrent request did not reach provider")
		}
	}
	if !keys["Bearer one"] || !keys["Bearer two"] {
		t.Fatalf("blank or untrimmed keys used: %v", keys)
	}
	for range 2 {
		release <- struct{}{}
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	client = newDecisionsClient([]string{" ", ""}, "model", server.URL, server.Client())
	if _, err := client.Decide(context.Background(), decisionTestRequest()); err == nil {
		t.Fatal("blank keys treated as configuration")
	}
}

func TestDecisionsDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := NewDecisionsClient([]string{"key"}, "model")
	client.endpoint = server.URL
	if _, err := client.Decide(context.Background(), decisionTestRequest()); err == nil || leaked.Load() {
		t.Fatalf("redirect followed: %v", err)
	}
}
