# Persisted Garmin base system prompt

Garmin reads its base instructions from the SQLite setting
`garmin_base_system_prompt` for each new general-chat agent request. Stored overrides survive
restarts. The bot has no command, HTTP editor or web interface to replace or reset
the base prompt.

Without an override, the bot uses `cmd.GarminSystemPrompt()`. The storage API
has `GetGarminSystemPrompt`, `SetGarminSystemPrompt` and
`ResetGarminSystemPrompt`. Reset deletes the setting and restores the current
built-in default. Writes are atomic and concurrent saves are last-write-wins.
Already-running requests retain the prompt they started with. A database read
error stops a request instead of silently using stale/default instructions.
Back up the database to preserve an override.

## Scope

The override replaces the general-chat base used by `runGarminAIWithMode`.
App-support uses separate evidence-focused instructions and does not load this
override or shared memory. Neither silent app-support triage nor conversation
summarization uses the override. Runtime API model identity is still
appended by the model client. The bot appends shared memory as lower-priority data,
never instructions. Discord context, loaded skills, tool selection and
code-enforced authorization are unchanged. Prompt text cannot grant tool access
or change credentials/model configuration.

## Storage validation

Replacement content must be nonblank valid UTF-8 and at most 64 KiB. Storage
preserves text exactly, including whitespace, newlines and literal escape
sequences; the model client trims leading/trailing whitespace at
inference time. Invalid content and failed writes leave the previous override
intact. Reset is idempotent. A stored override remains in the database when AI is
disabled and is used when AI is enabled again.

Prompt text is sent to the configured AI provider during inference. Do not put
passwords, tokens or other secrets in it.
