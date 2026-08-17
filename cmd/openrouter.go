package cmd

import (
	"net/http"
	"strings"
	"time"
)

const (
	openRouterEndpoint      = "https://openrouter.ai/api/v1/chat/completions"
	openRouterDefaultModel  = "qwen/qwen3.7-flash"
	openRouterPreviousModel = "openai/gpt-5.4-nano"
	openRouterOlderModel    = "openai/gpt-5-mini"
	openRouterLegacyDefault = "upstage/solar-pro4"
	openRouterBrokenDefault = "ibm-granite/granite-4.1-8b"
	openRouterSessionID     = "metrobot-qwen3.7-flash-v1"
)

type OpenRouterClient struct {
	*chatCompletionClient
}

func NewOpenRouterClient(keys []string, model string) *OpenRouterClient {
	return newOpenRouterClient(keys, model, openRouterEndpoint, &http.Client{Timeout: 20 * time.Second})
}

func newOpenRouterClient(keys []string, model, endpoint string, httpClient *http.Client) *OpenRouterClient {
	model = strings.TrimSpace(model)
	useDefaultRoute := model == "" || model == openRouterDefaultModel || model == openRouterPreviousModel || model == openRouterOlderModel || model == openRouterLegacyDefault || model == openRouterBrokenDefault
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
			if useDefaultRoute {
				request.Reasoning = &chatReasoning{Effort: "minimal", Exclude: true}
				request.SessionID = openRouterSessionID
				if len(request.Messages) > 0 {
					request.Messages[0].Cache = true
				}
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
