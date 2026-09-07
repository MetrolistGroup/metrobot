package firecrawl

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
	"unicode/utf8"
)

const (
	apiEndpoint       = "https://api.firecrawl.dev/v2/search"
	failedKeyCooldown = 48 * time.Hour
)

type Client struct {
	keys        []string
	failedUntil []atomic.Int64
	endpoint    string
	httpClient  *http.Client
	nextKey     atomic.Uint64
}

type searchRequest struct {
	Query         string         `json:"query"`
	Limit         int            `json:"limit"`
	Sources       []string       `json:"sources,omitempty"`
	ScrapeOptions *scrapeOptions `json:"scrapeOptions,omitempty"`
}

type scrapeOptions struct {
	Formats []string `json:"formats"`
}

func NewClient(keys []string) *Client {
	return newClient(keys, apiEndpoint, &http.Client{Timeout: 20 * time.Second})
}

func newClient(keys []string, endpoint string, httpClient *http.Client) *Client {
	cleanKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			cleanKeys = append(cleanKeys, key)
		}
	}
	return &Client{keys: cleanKeys, failedUntil: make([]atomic.Int64, len(cleanKeys)), endpoint: endpoint, httpClient: httpClient}
}

func (c *Client) Search(ctx context.Context, query string, limit int, source string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("web search query is required")
	}
	if utf8.RuneCountInString(query) > 500 {
		return "", fmt.Errorf("web search query cannot exceed 500 characters")
	}
	if limit == 0 {
		limit = 3
	}
	if limit < 1 || limit > 5 {
		return "", fmt.Errorf("web search limit must be between 1 and 5")
	}
	if len(c.keys) == 0 {
		return "", fmt.Errorf("no Firecrawl API keys configured")
	}

	request := searchRequest{Query: query, Limit: limit}
	switch source = strings.ToLower(strings.TrimSpace(source)); source {
	case "", "web":
		request.ScrapeOptions = &scrapeOptions{Formats: []string{"markdown"}}
	case "news":
		request.Sources = []string{"news"}
		request.ScrapeOptions = &scrapeOptions{Formats: []string{"markdown"}}
	case "images":
		request.Sources = []string{"images"}
	default:
		return "", fmt.Errorf("web search source must be web, news, or images")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encoding Firecrawl request: %w", err)
	}

	start := int((c.nextKey.Add(1) - 1) % uint64(len(c.keys)))
	now := time.Now().Unix()
	var lastErr error
	for attempt := range len(c.keys) {
		keyIndex := (start + attempt) % len(c.keys)
		if c.failedUntil[keyIndex].Load() > now {
			continue
		}
		body, status, err := c.searchWithKey(ctx, payload, c.keys[keyIndex])
		if err != nil {
			if ctx.Err() != nil {
				return "", fmt.Errorf("calling Firecrawl: %w", ctx.Err())
			}
			lastErr = err
			continue
		}
		if status >= 200 && status < 300 {
			var response struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				return "", fmt.Errorf("decoding Firecrawl response: %w", err)
			}
			if !response.Success {
				if response.Error == "" {
					response.Error = "search failed"
				}
				return "", fmt.Errorf("Firecrawl: %s", response.Error)
			}
			return string(body), nil
		}

		var response struct {
			Error   string          `json:"error"`
			Details json.RawMessage `json:"details"`
		}
		_ = json.Unmarshal(body, &response)
		message := strings.TrimSpace(response.Error)
		if message == "" {
			message = http.StatusText(status)
		}
		if details := strings.TrimSpace(string(response.Details)); details != "" && details != "null" {
			message += ": " + details
		}
		lastErr = fmt.Errorf("Firecrawl returned %d: %s", status, message)
		if keyFailureStatus(status) {
			c.failedUntil[keyIndex].Store(time.Now().Add(failedKeyCooldown).Unix())
		}
		if !retryStatus(status) {
			return "", lastErr
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all Firecrawl API keys are cooling down")
	}
	return "", lastErr
}

func (c *Client) searchWithKey(ctx context.Context, payload []byte, key string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("creating Firecrawl request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("calling Firecrawl: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("reading Firecrawl response: %w", err)
	}
	return body, response.StatusCode, nil
}

func retryStatus(status int) bool {
	return keyFailureStatus(status) || status == http.StatusRequestTimeout || status >= http.StatusInternalServerError
}

func keyFailureStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}
