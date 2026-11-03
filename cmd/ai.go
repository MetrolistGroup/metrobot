package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const chatCompletionAttemptTimeout = 15 * time.Second

const (
	chatCompletionRateLimitRetries = 3
	chatCompletionTransportRetries = 1
	chatCompletionRateLimitDelay   = time.Second
	chatCompletionRequestMax       = 16 << 20
	chatCompletionResponseMax      = 1 << 20
)

type GarminAI interface {
	Complete(ctx context.Context, request GarminAIRequest) (*GarminAICompletion, error)
}

type GarminAIRequest struct {
	DisableReasoning bool
	SystemPrompt     string
	Context          string
	Messages         []GarminAIMessage
	Tools            []GarminAITool
	ToolChoice       string
}

type GarminAIMessage struct {
	Timestamp        time.Time `json:"-"`
	Reasoning        string
	Role             string             `json:"role"`
	Name             string             `json:"name,omitempty"`
	Content          string             `json:"content"`
	Images           []string           `json:"-"`
	ToolCalls        []GarminAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string             `json:"tool_call_id,omitempty"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	ReasoningDetails json.RawMessage    `json:"reasoning_details,omitempty"`
	Cache            bool               `json:"-"`
}

// MarshalJSON sends null content for assistant tool calls, as required by
// OpenAI-compatible chat APIs. It uses content parts only for images and
// provider prompt-cache breakpoints, keeping ordinary messages as strings.
func (m GarminAIMessage) MarshalJSON() ([]byte, error) {
	type wireMessage struct {
		Role             string             `json:"role"`
		Name             string             `json:"name,omitempty"`
		Content          any                `json:"content"`
		ToolCalls        []GarminAIToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string             `json:"tool_call_id,omitempty"`
		Reasoning        string             `json:"reasoning,omitempty"`
		ReasoningContent string             `json:"reasoning_content,omitempty"`
		ReasoningDetails json.RawMessage    `json:"reasoning_details,omitempty"`
	}

	var content any
	if m.Role == "assistant" && m.Content == "" && len(m.ToolCalls) > 0 {
		content = nil
	} else if m.Cache || len(m.Images) > 0 {
		parts := make([]chatContentPart, 0, len(m.Images)+1)
		if m.Content != "" || len(m.Images) == 0 {
			part := chatContentPart{Type: "text", Text: m.Content}
			if m.Cache {
				part.CacheControl = &chatCacheControl{Type: "ephemeral"}
			}
			parts = append(parts, part)
		}
		for _, imageURL := range m.Images {
			if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
				parts = append(parts, chatContentPart{
					Type:     "image_url",
					ImageURL: &chatImageURL{URL: imageURL},
				})
			}
		}
		content = parts
	} else {
		content = m.Content
	}
	return json.Marshal(wireMessage{
		Role:             m.Role,
		Name:             m.Name,
		Content:          content,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
		Reasoning:        m.Reasoning,
		ReasoningContent: m.ReasoningContent,
		ReasoningDetails: m.ReasoningDetails,
	})
}

// UnmarshalJSON accepts both ordinary string content and multimodal content
// arrays so test servers and provider responses can use the same message type.
func (m *GarminAIMessage) UnmarshalJSON(data []byte) error {
	type wireMessage struct {
		Role             string             `json:"role"`
		Name             string             `json:"name,omitempty"`
		Content          json.RawMessage    `json:"content"`
		ToolCalls        []GarminAIToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string             `json:"tool_call_id,omitempty"`
		Reasoning        string             `json:"reasoning,omitempty"`
		ReasoningContent string             `json:"reasoning_content,omitempty"`
		ReasoningDetails json.RawMessage    `json:"reasoning_details,omitempty"`
	}
	var wire wireMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.Role = wire.Role
	m.Name = wire.Name
	m.ToolCalls = wire.ToolCalls
	m.ToolCallID = wire.ToolCallID
	m.Reasoning = wire.Reasoning
	m.ReasoningContent = wire.ReasoningContent
	m.ReasoningDetails = wire.ReasoningDetails
	m.Content = ""
	m.Images = nil
	m.Cache = false
	m.Timestamp = time.Time{}
	if len(wire.Content) == 0 || bytes.Equal(wire.Content, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(wire.Content, &m.Content); err == nil {
		return nil
	}
	var parts []chatContentPart
	if err := json.Unmarshal(wire.Content, &parts); err != nil {
		return fmt.Errorf("decoding message content: %w", err)
	}
	for _, part := range parts {
		switch part.Type {
		case "text":
			m.Content += part.Text
			m.Cache = m.Cache || part.CacheControl != nil
		case "image_url":
			if part.ImageURL != nil && strings.TrimSpace(part.ImageURL.URL) != "" {
				m.Images = append(m.Images, strings.TrimSpace(part.ImageURL.URL))
			}
		}
	}
	return nil
}

type chatContentPart struct {
	Type         string            `json:"type"`
	Text         string            `json:"text,omitempty"`
	ImageURL     *chatImageURL     `json:"image_url,omitempty"`
	CacheControl *chatCacheControl `json:"cache_control,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

type chatCacheControl struct {
	Type string `json:"type"`
}

type GarminAIToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function GarminAIFunctionCall `json:"function"`
}

type GarminAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type GarminAITool struct {
	Type     string                     `json:"type"`
	Function GarminAIFunctionDefinition `json:"function"`
}

type GarminAIFunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict,omitempty"`
}

type GarminAICompletion struct {
	Message      GarminAIMessage
	FinishReason string
}

type chatCompletionClient struct {
	keys             []string
	endpoint         string
	model            string
	provider         string
	headers          map[string]string
	configureRequest func(*chatCompletionRequest)
	httpClient       *http.Client
	attemptTimeout   time.Duration
	rateLimitDelay   time.Duration
	nextKey          atomic.Uint64
}

type chatCompletionRequest struct {
	DisableReasoning bool                     `json:"-"`
	Model            string                   `json:"model,omitempty"`
	Models           []string                 `json:"models,omitempty"`
	SessionID        string                   `json:"session_id,omitempty"`
	Messages         []chatMessage            `json:"messages"`
	Reasoning        *chatReasoning           `json:"reasoning,omitempty"`
	Provider         *chatProviderPreferences `json:"provider,omitempty"`
	MaxTokens        int                      `json:"max_tokens"`
	Stream           bool                     `json:"stream"`
	Tools            []GarminAITool           `json:"tools,omitempty"`
	ToolChoice       string                   `json:"tool_choice,omitempty"`
}

type chatMessage = GarminAIMessage

type chatReasoning struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

type chatProviderPreferences struct {
	ZDR               bool              `json:"zdr"`
	DataCollection    string            `json:"data_collection,omitempty"`
	RequireParameters bool              `json:"require_parameters"`
	Sort              chatProviderSort  `json:"sort"`
	MaxPrice          chatProviderPrice `json:"max_price,omitempty"`
}

type chatProviderSort struct {
	By        string `json:"by"`
	Partition string `json:"partition,omitempty"`
}

type chatProviderPrice struct {
	Prompt     float64 `json:"prompt,omitempty"`
	Completion float64 `json:"completion,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
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
		attemptTimeout:   chatCompletionAttemptTimeout,
		rateLimitDelay:   chatCompletionRateLimitDelay,
	}
}

func (c *chatCompletionClient) Ask(ctx context.Context, messages []GarminAIMessage) (string, error) {
	completion, err := c.Complete(ctx, GarminAIRequest{Messages: messages})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(completion.Message.Content) == "" {
		return "", fmt.Errorf("%s returned no text response", c.provider)
	}
	return strings.TrimSpace(completion.Message.Content), nil
}

func (c *chatCompletionClient) Complete(ctx context.Context, input GarminAIRequest) (*GarminAICompletion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(c.keys) == 0 {
		return nil, fmt.Errorf("no %s API keys configured", c.provider)
	}
	if len(input.Messages) == 0 {
		return nil, fmt.Errorf("no messages provided")
	}
	systemPrompt := input.SystemPrompt
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = garminSystemPrompt
	}
	// Caller system prompts can include memory and skills; only the public built-in prefix is safe to mark automatically.
	dynamicPrompt := ""
	if strings.HasPrefix(systemPrompt, garminSystemPrompt+"\n") {
		dynamicPrompt = strings.TrimPrefix(systemPrompt, garminSystemPrompt)
		systemPrompt = garminSystemPrompt
	}
	cacheSystemPrompt := systemPrompt == garminSystemPrompt
	systemPrompt += "\n\nRuntime model identity:\n- The exact API model powering this response is `" + c.model + "`.\n- If asked what model you are, state this exact model ID. You are still Metrobot, the Discord bot; do not claim to be a different model or provider."

	messageCapacity := len(input.Messages) + 2
	if strings.TrimSpace(input.Context) != "" {
		messageCapacity++
	}
	request := chatCompletionRequest{
		DisableReasoning: input.DisableReasoning,
		Model:            c.model,
		Messages:         make([]chatMessage, 1, messageCapacity),
		MaxTokens:        768,
		Stream:           false,
		Tools:            append([]GarminAITool(nil), input.Tools...),
	}
	for i := range request.Tools {
		request.Tools[i].Function.Parameters = append(json.RawMessage(nil), request.Tools[i].Function.Parameters...)
	}
	if len(input.Tools) > 0 {
		request.ToolChoice = input.ToolChoice
		if request.ToolChoice == "" {
			request.ToolChoice = "auto"
		}
	}
	request.Messages[0] = chatMessage{Role: "system", Content: systemPrompt}
	if dynamicPrompt != "" {
		request.Messages = append(request.Messages, chatMessage{Role: "system", Content: dynamicPrompt})
	}
	if strings.TrimSpace(input.Context) != "" {
		request.Messages = append(request.Messages, chatMessage{Role: "system", Content: input.Context})
	}
	for _, message := range input.Messages {
		message.Images = append([]string(nil), message.Images...)
		message.ToolCalls = append([]GarminAIToolCall(nil), message.ToolCalls...)
		message.ReasoningDetails = append(json.RawMessage(nil), message.ReasoningDetails...)
		request.Messages = append(request.Messages, message)
	}
	if c.configureRequest != nil {
		c.configureRequest(&request)
	}
	if !cacheSystemPrompt {
		request.Messages[0].Cache = false
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encoding %s request failed", c.provider)
	}
	if len(payload) > chatCompletionRequestMax {
		return nil, fmt.Errorf("oversized %s request", c.provider)
	}

	start := int((c.nextKey.Add(1) - 1) % uint64(len(c.keys)))
	keyAttempts := 0
	rateLimitRetries := 0
	transportRetries := 0
	retryAt := make([]time.Time, len(c.keys))
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("calling %s: %w", c.provider, err)
		}
		keyIndex := (start + keyAttempts) % len(c.keys)
		if keyAttempts >= len(c.keys) {
			// Reuse an available key before waiting for a throttled one.
			for offset := 1; offset < len(c.keys); offset++ {
				candidate := (start + keyAttempts + offset) % len(c.keys)
				if retryAt[candidate].Before(retryAt[keyIndex]) {
					keyIndex = candidate
				}
			}
		}
		if err := waitForChatCompletionRetry(ctx, time.Until(retryAt[keyIndex])); err != nil {
			return nil, fmt.Errorf("calling %s: %w", c.provider, err)
		}
		keyAttempts++
		attemptCtx, cancel := context.WithTimeout(ctx, c.attemptTimeout)
		completion, retry, err := c.askWithKey(attemptCtx, payload, c.keys[keyIndex])
		attemptErr := attemptCtx.Err()
		cancel()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("calling %s: %w", c.provider, ctx.Err())
		}
		if err == nil {
			return completion, nil
		}
		if attemptErr != nil {
			return nil, err
		}
		rateLimited := chatCompletionStatus(err) == http.StatusTooManyRequests
		if rateLimited {
			delay := c.rateLimitDelay
			var httpErr *chatCompletionHTTPError
			if errors.As(err, &httpErr) && httpErr.retryAfter > delay {
				delay = httpErr.retryAfter
			}
			retryAt[keyIndex] = time.Now().Add(delay)
		}
		// Exhausted retry budgets must not suppress an untouched key.
		if retry && keyAttempts < len(c.keys) {
			if rateLimited {
				rateLimitRetries++
			} else if isChatCompletionTransportError(err) {
				transportRetries++
			}
			continue
		}
		if rateLimited {
			if rateLimitRetries >= chatCompletionRateLimitRetries {
				return nil, err
			}
			rateLimitRetries++
			continue
		}
		if isChatCompletionTransportError(err) {
			if transportRetries >= chatCompletionTransportRetries {
				return nil, err
			}
			transportRetries++
			if err := waitForChatCompletionRetry(ctx, c.rateLimitDelay); err != nil {
				return nil, fmt.Errorf("calling %s: %w", c.provider, err)
			}
			continue
		}
		if !retry || keyAttempts >= len(c.keys) {
			return nil, err
		}
	}
}

type chatCompletionHTTPError struct {
	status     int
	retryAfter time.Duration
	err        error
}

type chatCompletionTransportError struct {
	err error
}

func (e *chatCompletionHTTPError) Error() string      { return e.err.Error() }
func (e *chatCompletionHTTPError) Unwrap() error      { return e.err }
func (e *chatCompletionTransportError) Error() string { return e.err.Error() }
func (e *chatCompletionTransportError) Unwrap() error { return e.err }

func isChatCompletionTransportError(err error) bool {
	var transportErr *chatCompletionTransportError
	return errors.As(err, &transportErr)
}

func chatCompletionStatus(err error) int {
	var httpErr *chatCompletionHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.status
	}
	return 0
}

func waitForChatCompletionRetry(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	if deadline, ok := ctx.Deadline(); ok && delay >= time.Until(deadline) {
		return context.DeadlineExceeded
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *chatCompletionClient) askWithKey(ctx context.Context, payload []byte, key string) (*GarminAICompletion, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, false, fmt.Errorf("creating %s request failed", c.provider)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, true, fmt.Errorf("calling %s: %w", c.provider, ctxErr)
		}
		return nil, true, &chatCompletionTransportError{err: fmt.Errorf("calling %s: transport failed", c.provider)}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, retryChatCompletionStatus(resp.StatusCode), &chatCompletionHTTPError{
			status:     resp.StatusCode,
			retryAfter: chatCompletionRetryAfter(resp.Header.Get("Retry-After")),
			err:        fmt.Errorf("%s returned HTTP status %d", c.provider, resp.StatusCode),
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, chatCompletionResponseMax+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, fmt.Errorf("reading %s response: %w", c.provider, ctx.Err())
		}
		return nil, true, &chatCompletionTransportError{err: fmt.Errorf("reading %s response failed", c.provider)}
	}
	if len(body) > chatCompletionResponseMax {
		return nil, false, fmt.Errorf("oversized %s response", c.provider)
	}
	var result chatCompletionResponse
	if json.Unmarshal(body, &result) != nil || result.Error != nil {
		return nil, false, fmt.Errorf("invalid %s response", c.provider)
	}
	if len(result.Choices) == 0 {
		return nil, false, fmt.Errorf("%s returned no choices", c.provider)
	}
	message := result.Choices[0].Message
	var reasoningDetails []json.RawMessage
	if message.Role != "assistant" || (len(message.ReasoningDetails) > 0 && json.Unmarshal(message.ReasoningDetails, &reasoningDetails) != nil) {
		return nil, false, fmt.Errorf("invalid %s assistant message", c.provider)
	}
	seenCalls := make(map[string]bool, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		var arguments map[string]json.RawMessage
		validArguments := json.Unmarshal([]byte(call.Function.Arguments), &arguments) == nil && arguments != nil
		if strings.TrimSpace(call.ID) == "" || seenCalls[call.ID] || call.Type != "function" || strings.TrimSpace(call.Function.Name) == "" || !validArguments {
			return nil, false, fmt.Errorf("%s returned an invalid tool call", c.provider)
		}
		seenCalls[call.ID] = true
	}
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 && strings.TrimSpace(message.Reasoning) == "" && strings.TrimSpace(message.ReasoningContent) == "" && len(reasoningDetails) == 0 {
		return nil, false, fmt.Errorf("%s returned an empty response", c.provider)
	}

	return &GarminAICompletion{
		Message:      message,
		FinishReason: result.Choices[0].FinishReason,
	}, false, nil
}

func chatCompletionRetryAfter(value string) time.Duration {
	if seconds, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(deadline))
	}
	return 0
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
