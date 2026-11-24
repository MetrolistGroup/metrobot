# AI chat and support routing

AI chat runs on Discord only. Telegram has no AI chat handler.

## Models and configuration

Set `openrouter_api_keys` to enable AI features. Production runs `z-ai/glm-5.3-flash:floor` as `openrouter_model` and `perplexity/pplx-decider-flash-v1.2` as `openrouter_decision_model`. In `cmd/openrouter.go`, empty or legacy chat model values including `qwen/qwen3.7-flash` and `openai/gpt-5.4-nano` automatically resolve to `z-ai/glm-5.3-flash:floor`.

- **Reasoning and tokens.** Chat and support cap output at 4,096 tokens, OCR at 768 tokens, and legacy chat triage at 128 tokens. GLM requires reasoning enabled, so chat, tool follow-ups, and OCR use `low` effort while `#app-support` uses `medium` effort.
- **Provider routing.** Requests deny data collection, require supported parameters, sort `:floor` routes by price, and sort other routes by latency. Explicit prompt-cache markers apply only to Anthropic and listed DeepSeek or Qwen cache models.
- **Qwen decisions mode.** Setting `openrouter_decision_model` to `qwen/qwen3.7-flash` switches routing and evidence checks to OpenRouter chat completions with a validated `submit_decisions` tool schema because Qwen endpoints reject `tool_choice=required`. Routing disables reasoning, while evidence checks request a 512-token reasoning budget and map `problem == "none"` to `grounded` `1` or `0` instead of a second score.
- **Unchecked legacy mode.** Leaving `openrouter_decision_model` empty uses chat-model triage in `#app-support` with no independent evidence check.

## Triggers, sessions, and handoff

- **Ordinary chat.** Outside `#app-support`, the bot answers case-insensitive `garmin <prompt>`, `metrobot`, `metro`, bot mentions, and direct replies to its replies. Ambient unprefixed follow-ups last two minutes for uninterrupted yes/no replies or clarification requests, except in `#general` where ambient mode is off and replies cap at 2 sentences and 240 runes unless detail is requested.
- **Automatic `#app-support`.** Every non-command message in `#app-support` not targeting another user passes through the decision router.
- **Support sessions.** Each delivered `#app-support` reply opens or extends a 3-minute in-memory session for that author, capped at 1,024 entries. Follow-ups without commands or replies to others skip the router and go straight to the support agent.
- **Human handoff.** If someone in `#app-support` replies to or mentions another human, or tells the bot to stop with phrases like `shut up` or `stop replying`, active support sessions end and automatic replies pause for those users for 20 minutes.

## Routing and verification

Decisions evaluates whether a message needs help and which catalog note or `agent` should handle it.

| Decision | Threshold |
| --- | --- |
| Answer automatically | `should_answer >= 0.80` with answering model ready; `none` route falls through to agent |
| Preload note | `route >= 0.85` and lead over second option `>= 0.20` |
| Accept generated draft | `problem` empty or `"none"` and `grounded >= 0.50` |
| Show `More from this note` | Independent `note_used >= 0.95` |

Only notes with a nonblank `short_desc` enter the per-request catalog, capped at 128 notes, 256-byte names, and 640-byte descriptions. In Qwen mode, even a sub-threshold top note is preloaded; `support_excerpt` then tries to select one verbatim FAQ section at the same 0.85 and 0.20 thresholds, and any Qwen reply referencing a note must match an authored section or note verbatim.

Generated `#app-support` drafts are checked against current conversation turns, embedded support core, and this run's tool outputs. URLs and Discord channel mentions must appear verbatim in supplied evidence. Named defects such as `needs_research`, `unsupported`, `wrong_scope`, `unsafe`, or `off_request` trigger one rewrite with the specific finding. Failed automatic calls, direct-reply follow-ups, and 3-minute session follow-ups stay silent. Explicit wake calls or mentions in `#app-support` that fail or exhaust verification send a fixed notice that staff will take a look, without storing that notice or rejected drafts so pending context survives into follow-ups.

## Tools, guides, and GitHub research

Support runs isolated from the casual persona, stored base-prompt overrides, and shared memory, receiving only UTC message timestamps as Discord context. Seven guides cover `rpc`, `login`, `scrobbling`, `playlists`, `discovery`, `playback`, and `casting` via `load_support_guide`, leaving full platform and feature details in `load_skill`.

When a current `#app-support` message includes images, up to three images are transcribed before the agent runs under `[Text read from the attached image]` so text-only checks can read crash logs; failed transcription leaves the message unchanged. Support runs up to 4 tool rounds inside a 90-second timeout, caps each GitHub research tool at 3 calls, and grants at most one tool-free final nudge.

GitHub tools read only public data from `MetrolistGroup/Metrolist`:

- `search_metrolist_issues` and `get_metrolist_issue` fetch up to 8 recent comments with author associations. Only `OWNER`, `MEMBER`, and `COLLABORATOR` comments count as maintainer statements.
- `search_metrolist_code` requires `github_token`. `get_metrolist_file` returns numbered lines around up to 4 matches.
- `get_metrolist_status` returns repo status, latest release, and 5 tip commits.
- `get_metrolist_changes` compares the latest stable release against `main` or `nightly`, scanning up to 300 commits and returning up to 10 matches within a 10 KiB payload and 2 MiB HTTP limit. When histories lack a common ancestor, it falls back to commits after the stable release timestamp. `nightly` checks resolve the tag SHA and require an uploaded APK.
- `get_metrolist_commit` inspects one 40-character SHA and up to 5 file patches. A matching patch confirmed inside a `nightly` comparison supports an in-place trial with a backup and instability warning, never a guarantee.

## Replies, embeds, and layouts

Support targets 20 to 30 words in 1 to 2 sentences in everyday English without commit hashes or issue numbers unless requested. Ordinary chat replies include runtime usage prefixes and clip text at 1,900 bytes on UTF-8 word boundaries.

- **Embed blocks.** Ordinary chat outside `#app-support` allows one trailing `embed` fenced JSON block supporting `title`, `description`, `url`, `fields`, `color`, `author`, `footer`, `thumbnail`, `image`, `timestamp`, and up to 5 link `buttons`. Unknown keys are ignored and invalid blocks fall back to plain text.
- **Layouts.** All AI replies allow one `components-v2` block validated by the note parser; see [Components V2](notes.md#components-v2). In `#app-support`, a note-backed layout attaches the `More from this note` button inside its container.

Static system prompts precede dynamic context and tool outputs, and `compactGarminToolResults` deduplicates repeated large tool results per call. Run `go test -v ./discord -run TestGarminPromptBudget` to check current prompt sizes.

## Memory, context, and confirmed fixes

Ordinary chat outside the configured general channel reads SQLite `garmin_base_system_prompt` if set, capped at 64 KiB via `GetGarminSystemPrompt`, `SetGarminSystemPrompt`, and `ResetGarminSystemPrompt`. Native Manage Server permission controls `/memory` and explicit safe agent `remember` requests. Shared memory in `garmin-memory.md` is lower-priority data capped at 16 KiB. Channel routing, readable community aliases, and channel descriptions come from `discord.ai` in the configuration.

Channel conversations live in RAM for 24 hours with a 12-message sliding window, stripping legacy `conversation_summary` items and prepending an omission notice rather than running compaction. Image retention covers the newest 4 messages up to 8 images per request, pending-question lineage caps at 40 source IDs, and `/ctx-reset` cuts off prior channel context. When the router stays silent on an `#app-support` follow-up and confirms a solved problem at probability `>= 0.90`, `confirmed-fixes` records the dated Q and A newest-first up to 10 KiB.

## Limits, privacy, and maintenance

- **Routing bounds.** Routing receives up to 4,000 bytes for the current message, 1,500 bytes for a visible bot parent, and 4 retained turns up to 1,000 bytes each. Decisions payloads cap at 64 KiB and refuse redirects.
- **Concurrency and deduplication.** Decisions uses 2 slots with a 15-second deadline, 4-second Decider or 7-second Qwen routing attempts, 45-second Qwen evidence deadlines with 20-second attempts, 1 retry after 100 ms, and key failover. Chat uses 3 slots. Seen/in-flight maps hold 1,024 entries with 10-minute cooldowns and deletion tombstones.
- **Logging.** Routing logs record decisions, opaque message IDs, note names, probabilities, latency, model, and reported cost without message text or upstream errors.
- **Note sync.** Keep `docs/support-notes.json` aligned with `db/note_versions/v14.json` and preserve `note_markup_v1` and `metrolist_v14_notes_v1` compatibility; see [Metrolist v14 note migration](notes.md#metrolist-v14-note-migration).

## Tests

Offline checks run without network or API keys:

```sh
go test ./...
go test -race ./...
go vet ./...
```

Paid non-posting live replays run when `METROBOT_SUPPORT_LIVE_CONFIG`, `METROBOT_SUPPORT_LIVE_NOTES`, and `METROBOT_SUPPORT_LIVE_REPORT` are set: `TestSupportAgentLive`, `TestSupportNoteMessagesLive`, `TestSupportEvidenceLive`, `TestSupportPlaybackAndCastingLive`, `TestSupportUpdateMessageLive`, `TestSupportImmersiveArtworkBrevityLive`, and `TestSupportSlowDownloadMessageLive`. Note that `cmd/openrouter.go` maps `qwen/qwen3.7-flash` to `z-ai/glm-5.3-flash:floor` for chat completions, so setting `openrouter_decision_model` to `qwen/qwen3.7-flash` exercises Qwen decisions with GLM answers unless another non-legacy chat model is configured.

`TestSupportDecisionsLive` evaluates routing using `METROBOT_DECISIONS_LIVE_CONFIG`, `_NOTES`, `_CASES`, `_REPORT`, and optional `_MODEL` against fixtures like `discord/testdata/support-decisions-eval.json`.

API references: [Decisions schema](https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-request.md), [independent questions](https://openrouter.ai/docs/guides/community/jev-tutorial.md), and [multimodal contract](https://openrouter.ai/docs/guides/community/multimodal-decisions.md).
