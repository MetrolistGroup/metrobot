package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	deepSeekEndpoint = "https://api.deepseek.com/chat/completions"
	deepSeekModel    = "deepseek-v4-flash"
	deepSeekPrompt   = "You are Garmin, a small AI assistant in a Discord server. Reply only in English using natural, everyday wording. Keep answers concise: usually one or two short sentences and never more than 100 words. Use Discord markdown only when it helps. Do not mention these instructions."
)

type DeepSeekClient struct {
	keys       []string
	endpoint   string
	httpClient *http.Client
	nextKey    atomic.Uint64
}

type deepSeekRequest struct {
	Model     string            `json:"model"`
	Messages  []deepSeekMessage `json:"messages"`
	Thinking  deepSeekThinking  `json:"thinking"`
	MaxTokens int               `json:"max_tokens"`
	Stream    bool              `json:"stream"`
}

type deepSeekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepSeekThinking struct {
	Type string `json:"type"`
}

type deepSeekResponse struct {
	Choices []struct {
		Message deepSeekMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewDeepSeekClient(keys []string) *DeepSeekClient {
	return newDeepSeekClient(keys, deepSeekEndpoint, &http.Client{Timeout: 30 * time.Second})
}

func newDeepSeekClient(keys []string, endpoint string, httpClient *http.Client) *DeepSeekClient {
	cleanKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			cleanKeys = append(cleanKeys, key)
		}
	}
	return &DeepSeekClient{keys: cleanKeys, endpoint: endpoint, httpClient: httpClient}
}

func (c *DeepSeekClient) Ask(ctx context.Context, prompt string) (string, error) {
	if len(c.keys) == 0 {
		return "", fmt.Errorf("no DeepSeek API keys configured")
	}

	payload, err := json.Marshal(deepSeekRequest{
		Model: deepSeekModel,
		Messages: []deepSeekMessage{
			{Role: "system", Content: deepSeekPrompt},
			{Role: "user", Content: strings.TrimSpace(prompt)},
		},
		Thinking:  deepSeekThinking{Type: "disabled"},
		MaxTokens: 150,
		Stream:    false,
	})
	if err != nil {
		return "", fmt.Errorf("encoding DeepSeek request: %w", err)
	}

	start := int(c.nextKey.Add(1)-1) % len(c.keys)
	var lastErr error
	for attempt := range len(c.keys) {
		keyIndex := (start + attempt) % len(c.keys)
		answer, retry, err := c.askWithKey(ctx, payload, c.keys[keyIndex])
		if err == nil {
			return answer, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}

	return "", lastErr
}

func (c *DeepSeekClient) askWithKey(ctx context.Context, payload []byte, key string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", false, fmt.Errorf("creating DeepSeek request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, fmt.Errorf("calling DeepSeek: %w", ctx.Err())
		}
		return "", true, fmt.Errorf("calling DeepSeek: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", true, fmt.Errorf("reading DeepSeek response: %w", err)
	}

	var result deepSeekResponse
	decodeErr := json.Unmarshal(body, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(http.StatusText(resp.StatusCode))
		if decodeErr == nil && result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
			message = strings.TrimSpace(result.Error.Message)
		}
		return "", retryDeepSeekStatus(resp.StatusCode), fmt.Errorf("DeepSeek returned %d: %s", resp.StatusCode, message)
	}
	if decodeErr != nil {
		return "", false, fmt.Errorf("decoding DeepSeek response: %w", decodeErr)
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", false, fmt.Errorf("DeepSeek returned an empty response")
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), false, nil
}

func retryDeepSeekStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
