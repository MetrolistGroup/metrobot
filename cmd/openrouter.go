package cmd

import (
	"net/http"
	"strings"
	"time"
)

const (
	openRouterEndpoint       = "https://openrouter.ai/api/v1/chat/completions"
	openRouterDefaultModel   = "ibm-granite/granite-4.1-8b"
	openRouterLegacyDefault  = "upstage/solar-pro4"
	openRouterFallbackModel1 = "meta-llama/llama-3.1-8b-instruct"
	openRouterFallbackModel2 = "qwen/qwen-2.5-7b-instruct"
)

var openRouterDefaultModels = []string{
	openRouterDefaultModel,
	openRouterFallbackModel1,
	openRouterFallbackModel2,
}

type OpenRouterClient struct {
	*chatCompletionClient
}

func NewOpenRouterClient(keys []string, model string) *OpenRouterClient {
	return newOpenRouterClient(keys, model, openRouterEndpoint, &http.Client{Timeout: 20 * time.Second})
}

func newOpenRouterClient(keys []string, model, endpoint string, httpClient *http.Client) *OpenRouterClient {
	model = strings.TrimSpace(model)
	useFastRoute := model == "" || model == openRouterDefaultModel || model == openRouterLegacyDefault
	if useFastRoute {
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
			if useFastRoute {
				request.Model = ""
				request.Models = append([]string(nil), openRouterDefaultModels...)
				request.Provider = &chatProviderPreferences{
					ZDR:               true,
					DataCollection:    "deny",
					RequireParameters: true,
					Sort: chatProviderSort{
						By:        "throughput",
						Partition: "none",
					},
					MaxPrice: chatProviderPrice{
						Prompt:     0.10,
						Completion: 0.20,
					},
				}
			} else {
				request.Reasoning = &chatReasoning{Enabled: false}
			}
		},
		httpClient,
	)}
}
