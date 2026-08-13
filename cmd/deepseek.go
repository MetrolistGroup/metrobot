package cmd

import (
	"net/http"
	"time"
)

const (
	deepSeekEndpoint = "https://api.deepseek.com/chat/completions"
	deepSeekModel    = "deepseek-v4-flash"
)

type DeepSeekClient struct {
	*chatCompletionClient
}

func NewDeepSeekClient(keys []string) *DeepSeekClient {
	return newDeepSeekClient(keys, deepSeekEndpoint, &http.Client{Timeout: 20 * time.Second})
}

func newDeepSeekClient(keys []string, endpoint string, httpClient *http.Client) *DeepSeekClient {
	return &DeepSeekClient{newChatCompletionClient(
		keys,
		endpoint,
		deepSeekModel,
		"DeepSeek",
		nil,
		func(request *chatCompletionRequest) {
			request.Thinking = &chatThinking{Type: "disabled"}
		},
		httpClient,
	)}
}
