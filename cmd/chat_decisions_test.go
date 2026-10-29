package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const chatDecisionArguments = `{"should_answer":{"reason":"A direct question.","probability_true":0.99},"route":{"reason":"Nightly installation.","choice":"n0","probabilities":{"n0":0.9,"none":0}}}`

func chatDecisionFixture(arguments string) []byte {
	body, _ := json.Marshal(map[string]any{
		"model":   openRouterDefaultModel,
		"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": GarminAIMessage{ToolCalls: []GarminAIToolCall{{Function: GarminAIFunctionCall{Name: "submit_decisions", Arguments: arguments}}}}}},
		"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 40, "cost": 0.001},
	})
	return body
}

func TestQwenDecisionsWireContract(t *testing.T) {
	if client := NewDecisionsClient([]string{"key"}, openRouterDefaultModel); client.endpoint != openRouterEndpoint {
		t.Fatal("Qwen was sent to the Decider-only endpoint")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Model != openRouterDefaultModel || body.ToolChoice != "" || body.Reasoning == nil || body.Reasoning.Enabled == nil || *body.Reasoning.Enabled || body.Provider.DataCollection != "deny" || !body.Provider.RequireParameters {
			t.Errorf("unexpected Qwen request: %#v", body)
		}
		if len(body.Tools) != 1 || !body.Tools[0].Function.Strict || len(body.Messages) != 2 || body.Messages[1].Role != "user" || !strings.Contains(body.Messages[1].Content, "nightly") {
			t.Error("missing schema or untrusted state boundary")
		}
		_, _ = w.Write(chatDecisionFixture(chatDecisionArguments))
	}))
	defer server.Close()
	client := newDecisionsClient([]string{"key"}, openRouterDefaultModel, server.URL, server.Client())
	response, err := client.Decide(context.Background(), decisionTestRequest())
	if err != nil || !response.ValidFor(decisionTestRequest()) || *response.Answers["route"].Probabilities["n0"] != 1 || *response.Usage.InputTokens != 100 || response.Answers["should_answer"].Reason == "" {
		t.Fatalf("Qwen decision = %#v, %v", response, err)
	}
}

func TestQwenEvidenceUsesOneVerdict(t *testing.T) {
	request := DecisionRequest{State: "evidence", Questions: map[string]DecisionQuestion{
		"grounded": {Type: "noul", Instructions: "Use only evidence.", Criteria: map[string]string{"true": "Supported", "false": "Unsupported"}},
		"problem":  {Type: "choice", Criteria: map[string]string{"none": "Supported", "unsupported": "Invented"}},
	}}
	body, err := chatDecisionBody(openRouterDefaultModel, request)
	if err != nil {
		t.Fatal(err)
	}
	var wire chatCompletionRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire.Tools[0].Function.Parameters), `"grounded"`) || request.Questions["grounded"].Type != "noul" || wire.Reasoning.Enabled == nil || !*wire.Reasoning.Enabled {
		t.Fatal("evidence schema has conflicting verdicts, mutated input, or disabled thinking")
	}
	for _, choice := range []string{"none", "unsupported"} {
		args := `{"problem":{"reason":"Evidence checked.","choice":"` + choice + `","probabilities":{"none":0.5,"unsupported":0.5}}}`
		response, err := parseChatDecision(chatDecisionFixture(args), request)
		if err != nil || !response.ValidFor(request) || (*response.Answers["grounded"].Noul == 1) != (choice == "none") {
			t.Fatalf("choice=%s response=%v err=%v", choice, response, err)
		}
	}
}

func TestQwenDecisionsRetryMalformedVerdict(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		args := `{}`
		if calls == 2 {
			args = chatDecisionArguments
		}
		_, _ = w.Write(chatDecisionFixture(args))
	}))
	defer server.Close()
	client := newDecisionsClient([]string{"key"}, openRouterDefaultModel, server.URL, server.Client())
	response, err := client.Decide(context.Background(), decisionTestRequest())
	if err != nil || response == nil || calls != 2 {
		t.Fatalf("response=%v err=%v calls=%d", response, err, calls)
	}
}

func TestQwenDecisionsRejectInvalidVerdicts(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"bad JSON", `"probability_true":0.99`, `"probability_true":`},
		{"missing probability", `"probability_true":0.99`, `"unused":0.99`},
		{"out of range", `"probability_true":0.99`, `"probability_true":2`},
		{"negative weight", `"n0":0.9`, `"n0":-0.1`},
		{"empty distribution", `"n0":0.9`, `"n0":0`},
		{"missing option", `,"none":0`, ``},
		{"extra option", `"none":0`, `"none":0,"other":0.1`},
		{"wrong winner", `"choice":"n0"`, `"choice":"none"`},
		{"unknown choice", `"choice":"n0"`, `"choice":"invented"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := chatDecisionFixture(strings.Replace(chatDecisionArguments, test.old, test.replacement, 1))
			if _, err := parseChatDecision(body, decisionTestRequest()); err == nil {
				t.Fatal("accepted invalid verdict")
			}
		})
	}
	for _, change := range []struct{ old, replacement string }{
		{`"submit_decisions"`, `"unknown_tool"`},
		{`"qwen/qwen3.7-flash"`, `"another/model"`},
		{`"tool_calls","message"`, `"length","message"`},
		{`"prompt_tokens":100`, `"prompt_tokens":null`},
	} {
		body := strings.Replace(string(chatDecisionFixture(chatDecisionArguments)), change.old, change.replacement, 1)
		if _, err := parseChatDecision([]byte(body), decisionTestRequest()); err == nil {
			t.Fatalf("accepted invalid envelope: %s", change.old)
		}
	}
}
