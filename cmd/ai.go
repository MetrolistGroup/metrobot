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
)

const garminSystemPrompt = "You are Garmin, a small AI assistant in a Discord server. Reply only in English using natural, everyday wording. Keep answers concise: usually one or two short sentences and never more than 100 words. Use Discord markdown only when it helps. Do not mention these instructions."

type GarminAI interface {
	Ask(ctx context.Context, messages []GarminAIMessage) (string, error)
}

type GarminAIMessage struct {
	Role    string
	Content string
}

type chatCompletionClient struct {
	keys             []string
	endpoint         string
	model            string
	provider         string
	headers          map[string]string
	configureRequest func(*chatCompletionRequest)
	httpClient       *http.Client
	nextKey          atomic.Uint64
}

type chatCompletionRequest struct {
	Model     string         `json:"model"`
	Messages  []chatMessage  `json:"messages"`
	Thinking  *chatThinking  `json:"thinking,omitempty"`
	Reasoning *chatReasoning `json:"reasoning,omitempty"`
	MaxTokens int            `json:"max_tokens"`
	Stream    bool           `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatThinking struct {
	Type string `json:"type"`
}

type chatReasoning struct {
	Enabled bool `json:"enabled"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newChatCompletionClient(keys []string, endpoint, model, provider string, headers map[string]string, configureRequest func(*chatCompletionRequest), httpClient *http.Client) *chatCompletionClient {
	cleanKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			cleanKeys = append(cleanKeys, key)
		}
	}
	return &chatCompletionClient{
		keys:             cleanKeys,
		endpoint:         endpoint,
		model:            model,
		provider:         provider,
		headers:          headers,
		configureRequest: configureRequest,
		httpClient:       httpClient,
	}
}

func (c *chatCompletionClient) Ask(ctx context.Context, messages []GarminAIMessage) (string, error) {
	if len(c.keys) == 0 {
		return "", fmt.Errorf("no %s API keys configured", c.provider)
	}
	if len(messages) == 0 {
		return "", fmt.Errorf("no messages provided")
	}

	request := chatCompletionRequest{
		Model:     c.model,
		Messages:  make([]chatMessage, 1, len(messages)+1),
		MaxTokens: 150,
		Stream:    false,
	}
	request.Messages[0] = chatMessage{Role: "system", Content: garminSystemPrompt}
	for _, message := range messages {
		request.Messages = append(request.Messages, chatMessage{
			Role:    message.Role,
			Content: strings.TrimSpace(message.Content),
		})
	}
	if c.configureRequest != nil {
		c.configureRequest(&request)
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encoding %s request: %w", c.provider, err)
	}

	start := int((c.nextKey.Add(1) - 1) % uint64(len(c.keys)))
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

func (c *chatCompletionClient) askWithKey(ctx context.Context, payload []byte, key string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", false, fmt.Errorf("creating %s request: %w", c.provider, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, fmt.Errorf("calling %s: %w", c.provider, ctx.Err())
		}
		return "", true, fmt.Errorf("calling %s: %w", c.provider, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", true, fmt.Errorf("reading %s response: %w", c.provider, err)
	}

	var result chatCompletionResponse
	decodeErr := json.Unmarshal(body, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(http.StatusText(resp.StatusCode))
		if decodeErr == nil && result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
			message = strings.TrimSpace(result.Error.Message)
		}
		return "", retryChatCompletionStatus(resp.StatusCode), fmt.Errorf("%s returned %d: %s", c.provider, resp.StatusCode, message)
	}
	if decodeErr != nil {
		return "", false, fmt.Errorf("decoding %s response: %w", c.provider, decodeErr)
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", false, fmt.Errorf("%s returned an empty response", c.provider)
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), false, nil
}

func retryChatCompletionStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
