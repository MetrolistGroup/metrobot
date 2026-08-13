# metrobot

this is the discord and telegram bot we use for logging, punishments, dehoisting, and group management

## Metrobot AI

Discord messages beginning with `garmin,` use a short AI response from Metrobot. Replying directly to Metrobot's response continues the conversation with limited recent context. Configure OpenRouter with `openrouter_api_keys` and optionally `openrouter_model`. The default route uses `openai/gpt-5-mini`, requires zero-data-retention endpoints and native tool support, and prioritizes throughput. The former `upstage/solar-pro4` and `ibm-granite/granite-4.1-8b` defaults are automatically migrated to this route. If no OpenRouter keys are configured, `deepseek_api_keys` enables direct DeepSeek V4 Flash instead.

Requests rotate across the configured keys, retry transient timeouts, and fall back from OpenRouter to DeepSeek when both are configured. Prompts and tool results are sent to the selected AI provider, so do not include private or sensitive information.

Metrobot can retrieve current Metrolist GitHub data, Discord member names, and all saved bot notes through read-only tools. It also has focused Metrolist and support skills. Tool and skill use is shown above each response.

Persistent context lives in `garmin-memory.md` by default. Set `garmin_memory_file` to use another path. The Docker image falls back to `/data/garmin-memory.md`; mount `/data` as a volume to retain updates when the container is replaced. Bot admins can manage it with `/memory view`, `/memory append`, `/memory replace`, and `/memory clear`; Metrobot may also append memory when an admin explicitly asks it to remember something.
