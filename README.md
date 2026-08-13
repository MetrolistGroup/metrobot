# metrobot

this is the discord and telegram bot we use for logging, punishments, dehoisting, and group management

## Garmin AI

Discord messages beginning with `garmin,` use a short AI response. Replying directly to Garmin's response continues the conversation with limited recent context. Configure OpenRouter with `openrouter_api_keys` and optionally `openrouter_model`; the default model is `upstage/solar-pro4`. If no OpenRouter keys are configured, `deepseek_api_keys` enables direct DeepSeek V4 Flash instead.

Requests rotate across the configured keys and fail over to another key when one is unavailable. Prompts are sent to the selected AI provider, so do not include private or sensitive information.
