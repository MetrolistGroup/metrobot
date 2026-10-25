package cmd

import (
	"net/http"
	"strings"
	"time"
)

const (
	openRouterEndpoint      = "https://openrouter.ai/api/v1/chat/completions"
	openRouterDefaultModel  = "qwen/qwen3.7-flash"
	openRouterPreviousQwen  = "qwen/qwen3.8-flash"
	openRouterPreviousModel = "openai/gpt-5.4-nano"
	openRouterOlderModel    = "openai/gpt-5-mini"
	openRouterLegacyDefault = "upstage/solar-pro4"
	openRouterBrokenDefault = "ibm-granite/granite-4.1-8b"
)

type OpenRouterClient struct {
	*chatCompletionClient
}

func NewOpenRouterClient(keys []string, model string) *OpenRouterClient {
	return newOpenRouterClient(keys, model, openRouterEndpoint, &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	})
}

func newOpenRouterClient(keys []string, model, endpoint string, httpClient *http.Client) *OpenRouterClient {
	model = strings.TrimSpace(model)
	useDefaultRoute := model == "" || model == openRouterDefaultModel || model == openRouterPreviousQwen || model == openRouterPreviousModel || model == openRouterOlderModel || model == openRouterLegacyDefault || model == openRouterBrokenDefault
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
			request.Provider = &chatProviderPreferences{
				DataCollection:    "deny",
				RequireParameters: true,
				Sort: chatProviderSort{
					By:        "latency",
					Partition: "none",
				},
			}
			if useDefaultRoute || request.DisableReasoning {
				disabled := false
				request.Reasoning = &chatReasoning{Enabled: &disabled}
			}
			// session_id is routing/trace grouping, not history; let OpenRouter derive it per conversation.
			if openRouterExplicitCacheModel(model) && len(request.Messages) > 0 {
				request.Messages[0].Cache = true
			}
		},
		httpClient,
	)}
}

// shortcut: automatic hints cover documented explicit-cache models only; extend when OpenRouter documents more.
func openRouterExplicitCacheModel(model string) bool {
	if strings.HasPrefix(model, "anthropic/claude-") {
		return true
	}
	switch model {
	case "deepseek/deepseek-v3.2", "qwen/qwen3-max", "qwen/qwen-plus", "qwen/qwen3.6-plus", "qwen/qwen3-coder-plus", "qwen/qwen3-coder-flash":
		return true
	default:
		return false
	}
}
