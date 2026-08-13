package cmd

import (
	"net/http"
	"strings"
	"time"
)

const (
	openRouterEndpoint     = "https://openrouter.ai/api/v1/chat/completions"
	openRouterDefaultModel = "upstage/solar-pro4"
)

type OpenRouterClient struct {
	*chatCompletionClient
}

func NewOpenRouterClient(keys []string, model string) *OpenRouterClient {
	return newOpenRouterClient(keys, model, openRouterEndpoint, &http.Client{Timeout: 30 * time.Second})
}

func newOpenRouterClient(keys []string, model, endpoint string, httpClient *http.Client) *OpenRouterClient {
	model = strings.TrimSpace(model)
	if model == "" {
		model = openRouterDefaultModel
	}
	return &OpenRouterClient{newChatCompletionClient(
		keys,
		endpoint,
		model,
		"OpenRouter",
		openRouterAttribution(model),
		map[string]string{
			"HTTP-Referer":       "https://github.com/MetrolistGroup/metrobot",
			"X-OpenRouter-Title": "Metrobot",
		},
		func(request *chatCompletionRequest) {
			request.Reasoning = &chatReasoning{Enabled: false}
		},
		httpClient,
	)}
}

func openRouterAttribution(model string) string {
	if model == openRouterDefaultModel {
		return "Upstage Solar Pro 4 via OpenRouter"
	}
	return model + " via OpenRouter"
}
