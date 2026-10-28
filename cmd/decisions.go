package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	decisionsEndpoint       = "https://openrouter.ai/api/alpha/decisions"
	DecisionsBodyMax        = 64 << 10
	DecisionsTimeout        = 8 * time.Second
	decisionsAttemptTimeout = 4 * time.Second
)

type DecisionQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type DecisionRequest struct {
	State     any                         `json:"state"`
	Questions map[string]DecisionQuestion `json:"questions"`
}

type DecisionAnswer struct {
	Type          string              `json:"type"`
	Noul          *float64            `json:"noul,omitempty"`
	Choice        string              `json:"choice,omitempty"`
	Probabilities map[string]*float64 `json:"probabilities,omitempty"`
	Confidence    *float64            `json:"confidence,omitempty"`
}

type DecisionResponse struct {
	Model   string                    `json:"model"`
	Answers map[string]DecisionAnswer `json:"answers"`
	Usage   *struct {
		InputTokens  *int     `json:"input_tokens"`
		OutputTokens *int     `json:"output_tokens"`
		Cost         *float64 `json:"cost"`
	} `json:"usage"`
}

type Decisions interface {
	Decide(context.Context, DecisionRequest) (*DecisionResponse, error)
}

type DecisionsClient struct {
	keys     []string
	model    string
	endpoint string
	http     *http.Client
	next     atomic.Uint64
}

func NewDecisionsClient(keys []string, model string) *DecisionsClient {
	return newDecisionsClient(keys, model, decisionsEndpoint, &http.Client{
		// Never forward the authorization header or message to a redirect target.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	})
}

func newDecisionsClient(keys []string, model, endpoint string, client *http.Client) *DecisionsClient {
	cleanKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			cleanKeys = append(cleanKeys, key)
		}
	}
	return &DecisionsClient{keys: cleanKeys, model: strings.TrimSpace(model), endpoint: endpoint, http: client}
}

func (c *DecisionsClient) Decide(ctx context.Context, request DecisionRequest) (*DecisionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, DecisionsTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(c.keys) == 0 || c.model == "" {
		return nil, errors.New("Decisions is not configured")
	}
	body, err := json.Marshal(struct {
		DecisionRequest
		Model    string         `json:"model"`
		Provider map[string]any `json:"provider"`
	}{request, c.model, map[string]any{"data_collection": "deny", "require_parameters": true, "sort": "latency"}})
	if err != nil || len(body) > DecisionsBodyMax {
		return nil, errors.New("invalid or oversized Decisions request")
	}
	start := int((c.next.Add(1) - 1) % uint64(len(c.keys)))
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, decisionsAttemptTimeout)
		response, retry, err := c.attempt(attemptCtx, c.keys[(start+attempt)%len(c.keys)], body, request)
		cancelAttempt()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil || !retry || attempt == 1 {
			return response, err
		}
		delay := 100 * time.Millisecond
		var httpErr *chatCompletionHTTPError
		// Retry-After limits reuse of this key, not failover to another key.
		if c.keys[start] == c.keys[(start+1)%len(c.keys)] && errors.As(err, &httpErr) && httpErr.retryAfter > delay {
			delay = httpErr.retryAfter
		}
		if err := waitForChatCompletionRetry(ctx, delay); err != nil {
			return nil, err
		}
	}
	panic("unreachable")
}

func (c *DecisionsClient) attempt(ctx context.Context, key string, body []byte, request DecisionRequest) (*DecisionResponse, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, false, errors.New("invalid Decisions endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/MetrolistGroup/metrobot")
	req.Header.Set("X-OpenRouter-Title", "Metrobot")
	resp, err := c.http.Do(req)
	if err != nil {
		// Transport and upstream errors can contain URLs, credentials or echoed input.
		return nil, true, errors.New("Decisions transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500 ||
			(len(c.keys) > 1 && (resp.StatusCode == 401 || resp.StatusCode == 402 || resp.StatusCode == 403))
		return nil, retry, &chatCompletionHTTPError{
			status:     resp.StatusCode,
			retryAfter: chatCompletionRetryAfter(resp.Header.Get("Retry-After")),
			err:        fmt.Errorf("Decisions HTTP status %d", resp.StatusCode),
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, DecisionsBodyMax+1))
	if err != nil {
		return nil, true, errors.New("reading Decisions response failed")
	}
	if len(data) > DecisionsBodyMax {
		return nil, false, errors.New("oversized Decisions response")
	}
	var response DecisionResponse
	if json.Unmarshal(data, &response) != nil || !response.ValidFor(request) {
		return nil, false, errors.New("invalid Decisions response")
	}
	return &response, false, nil
}

func decisionProbability(p *float64) bool {
	return p != nil && !math.IsNaN(*p) && !math.IsInf(*p, 0) && *p >= 0 && *p <= 1
}

// ValidFor checks the envelope and every probability against the requested options.
func (response *DecisionResponse) ValidFor(request DecisionRequest) bool {
	if response == nil {
		return false
	}
	if response.Model == "" || len(response.Model) > 200 || strings.Contains(response.Model, "://") ||
		response.Usage == nil || response.Usage.InputTokens == nil || response.Usage.OutputTokens == nil ||
		*response.Usage.InputTokens < 0 || *response.Usage.OutputTokens < 0 || len(request.Questions) == 0 || len(response.Answers) != len(request.Questions) {
		return false
	}
	for _, r := range response.Model {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._:~-", r)) {
			return false
		}
	}
	if cost := response.Usage.Cost; cost != nil && (math.IsNaN(*cost) || math.IsInf(*cost, 0) || *cost < 0) {
		return false
	}
	for name, question := range request.Questions {
		answer, ok := response.Answers[name]
		if !ok || answer.Type != question.Type {
			return false
		}
		switch question.Type {
		case "noul":
			if !decisionProbability(answer.Noul) {
				return false
			}
		case "choice":
			if _, ok := question.Criteria[answer.Choice]; !ok || len(answer.Probabilities) != len(question.Criteria) {
				return false
			}
			if answer.Confidence != nil && !decisionProbability(answer.Confidence) {
				return false
			}
			var sum float64
			winner := answer.Probabilities[answer.Choice]
			if !decisionProbability(winner) {
				return false
			}
			for option := range question.Criteria {
				p := answer.Probabilities[option]
				if !decisionProbability(p) || *p > *winner {
					return false
				}
				sum += *p
			}
			if math.Abs(sum-1) > 0.01 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
