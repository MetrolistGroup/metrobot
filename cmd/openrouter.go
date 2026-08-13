package cmd

import (
	"net/http"
	"strings"
	"time"
)

const (
	openRouterEndpoint      = "https://openrouter.ai/api/v1/chat/completions"
	openRouterDefaultModel  = "openai/gpt-5-mini"
	openRouterLegacyDefault = "upstage/solar-pro4"
	openRouterBrokenDefault = "ibm-granite/granite-4.1-8b"
)

type OpenRouterClient struct {
	*chatCompletionClient
}

func NewOpenRouterClient(keys []string, model string) *OpenRouterClient {
	return newOpenRouterClient(keys, model, openRouterEndpoint, &http.Client{Timeout: 20 * time.Second})
}

func newOpenRouterClient(keys []string, model, endpoint string, httpClient *http.Client) *OpenRouterClient {
	model = strings.TrimSpace(model)
	useDefaultRoute := model == "" || model == openRouterDefaultModel || model == openRouterLegacyDefault || model == openRouterBrokenDefault
	if useDefaultRoute {
		model = openRouterDefaultModel
	}
	return &OpenRouterClient{newChatCompletionClient(
		keys,
		endpoint,
		model,
		"OpenRouter",
		map[string]string{
			"HTTP-Referer":       "https://github.com/MetrolistGroup/metrobot",
			"X-OpenRouter-Title": "Metrobot",
		},
		func(request *chatCompletionRequest) {
			request.Reasoning = &chatReasoning{Enabled: false}
			if useDefaultRoute {
				request.Provider = &chatProviderPreferences{
					DataCollection:    "deny",
					RequireParameters: true,
					Sort: chatProviderSort{
						By:        "throughput",
						Partition: "none",
					},
				}
			}
		},
		httpClient,
	)}
}
