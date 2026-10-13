# Persisted Garmin base system prompt

Garmin reads its base instructions from the existing SQLite setting
`garmin_base_system_prompt` for each new agent request. Stored overrides survive
restart and remain effective without a web interface. There is no exposed bot
command or HTTP editor for replacing or resetting the base prompt.

Without an override, the bot uses `cmd.GarminSystemPrompt()`. The storage API
retains `GetGarminSystemPrompt`, `SetGarminSystemPrompt` and
`ResetGarminSystemPrompt`; reset deletes the setting and restores the current
built-in default. Writes are atomic and concurrent saves are last-write-wins.
Already-running requests retain the prompt they started with. A database read
error stops a request instead of silently using stale/default instructions.
Back up the database to preserve an override.

## Scope

The override replaces the whole base used by `runGarminAIWithMode`, including the
support agent after handoff. It does not edit the separate silent app-support
triage or conversation summarization prompts. Runtime API model identity is still
appended by the model client. Shared memory stays appended as lower-priority data,
never instructions. Discord context, loaded skills, tool selection and
code-enforced authorization are unchanged. Prompt text cannot grant tool access
or change credentials/model configuration.

## Storage validation

Replacement content must be nonblank valid UTF-8 and no larger than **64 KiB in
bytes**. Storage preserves text exactly, including whitespace, newlines and
literal escape sequences; the model client trims leading/trailing whitespace at
inference time. Invalid content and failed writes leave the previous override
intact. Reset is idempotent. A stored override remains in the database when AI is
disabled and is used when AI is enabled again.

Prompt text is sent to the configured AI provider during inference. Do not put
passwords, tokens or other secrets in it.
