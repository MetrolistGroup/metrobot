# Live Garmin base system prompt

Open **System prompt** in the dashboard to edit Garmin's live base instructions or reset them to the bundled default. Sign in as a Discord user listed in `permaadmin_discord_ids`, following the dashboard OAuth and loopback setup in [README.md](../README.md#local-admin-dashboard). Ordinary bot admins can sign in but cannot read or change this prompt. No new configuration fields or credentials are needed.

Saving replaces the **base** system prompt for subsequent Garmin agent requests without rebuilding or restarting. The override is stored in the bot's existing SQLite database under one setting, `garmin_base_system_prompt`, and survives restart. Reset deletes that setting and restores the current built-in `cmd.GarminSystemPrompt()`. Back up the database to preserve an override. Concurrent saves are last-write-wins; reload before editing if another permanent admin may have changed it. Already running requests retain the prompt they started with.

## Scope

The override changes the base instructions used by `runGarminAIWithMode`, including the support agent after handoff. It does **not** edit the separate silent app-support triage prompt or the conversation summarization prompt. Runtime API model identity is still appended by the model client. Shared memory stays appended as lower-priority data, never instructions. Discord context, loaded skills, tool selection, and code-enforced tool authorization are unchanged. Editing this text cannot grant tool access or change credentials/model configuration.

A replacement replaces the whole base, rather than adding to the built-in text. Start from the `default_content` returned by `GET /api/prompt` if you want to retain its behavior and policies. Prompt text is sent to the configured AI provider as part of normal inference, so do not put passwords, tokens, or other secrets in it. A database read error stops a request instead of silently using stale/default instructions. When AI is disabled, editing and reset still work; the prompt will be used when AI is enabled again.

## API

`/api/state` is unchanged and does not include prompt content. The dashboard fetches `/api/prompt` lazily when opening the editor.

- `GET /api/prompt` returns `{ "content": "effective base", "default_content": "built-in base", "overridden": false }`. `overridden` reports whether a stored override exists, even if its content equals the default.
- `POST /api/prompt/replace` accepts `{ "content": "new base" }` and returns `{ "ok": true }`.
- `POST /api/prompt/reset` accepts `{}` and returns `{ "ok": true }`.

All three routes require an active dashboard session and configured permanent-admin access, rechecked on each request. Missing/expired sessions receive JSON 401 errors; nonadmins and ordinary bot admins receive JSON 403 errors. Writes require the existing `X-CSRF-Token` and same-origin checks. Use `Content-Type: application/json`. Bodies are bounded to 128 KiB, must be UTF-8 JSON objects, and cannot contain unknown fields or trailing JSON. Reset accepts no fields.

Replacement content must be nonblank valid UTF-8 and no larger than **64 KiB in bytes**. Storage preserves the submitted decoded text exactly, including whitespace, newlines, and literal escape sequences; there is no server-side newline trimming or normalization. A browser textarea may normalize line endings to LF; the existing model client trims leading/trailing whitespace at inference time. Invalid requests and failed writes leave the previous override intact. Reset is idempotent.

Responses retain the dashboard's security headers and no-cache policy. Configuration, secrets, and database error details are not exposed. Successful writes attempt a metadata-only moderation audit entry with the caller ID and action `prompt_replace` or `prompt_reset`, never prompt text. Audit logging is best-effort after the atomic setting write: an audit failure does not undo a saved change or report it as failed. No production configuration, deployment, or live database change is needed to implement this feature.
