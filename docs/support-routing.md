# Automatic app-support routing

`openrouter_decision_model` is optional and defaults to `""`. Empty keeps the existing 15-second chat-model handoff triage unchanged. To opt in, configure an OpenRouter Decisions model alongside `openrouter_api_keys`, for example:

```json
{
  "openrouter_decision_model": "perplexity/pplx-decider-flash-v1.2"
}
```

This changes only **unprompted messages in the configured guild's app-support channel**. Explicit bot prompts, `.note`/`.notes` and slash commands retain their existing behavior. Clearing the setting and restarting rolls back to legacy triage. There are no new dependencies, tables, migrations or persistent routing statistics.

## Routing

The client posts to `https://openrouter.ai/api/alpha/decisions`, not chat completions. In one request it asks two **independent** questions:

- `should_answer` (Noul): is this clear Metrolist help seeking that an automatic answer can usefully address?
- `route` (Choice): which currently eligible saved note fits, or `agent` / `none`?

Neither question assumes it can read the other's answer. Both exclude chatter, keyword-only mentions, reactions, thanks, advice to others, staff coordination, human-directed conversation, donation proof and staff-only role/access requests. There is no keyword-triggered auto-answer rule. Bots, webhooks, other guilds/channels, command prefixes (`.`, `!`, `/`), mentions of other users and replies to humans are filtered before classification. Replies to this bot are allowed, including known retained bot replies whose parent is unavailable in the event; unknown reply targets are ignored conservatively.

A reply requires `should_answer >= 0.90`, winning route probability `>= 0.85`, and winner/runner-up margin `>= 0.20`. A high-confidence note route sends the exact current note through the existing Discord note renderer, with mentions suppressed, and remembers the actual rendered text for follow-ups. A high-confidence `agent` route uses the existing constrained support agent, without another chat-model triage pass. That agent can use exact eligible notes, the existing support skill and verified completed official issues. Its errors/busy state stay silent. `none`, ambiguity, invalid responses and API errors stay silent, with **no chat-model fallback on Decisions failure** and no generated routing menu. Authored FAQ notes still use their existing renderer.

These thresholds are **provisional, not calibrated accuracy guarantees**. A high model probability does not establish that a diagnosis or fix is correct. No crash-signature diagnosis or new data-clearing instructions are implemented.

## Note opt-in and changes

The catalog is rebuilt from `Notes.DB.ListNotes()` on every classification. Only notes with a **nonblank `short_desc`** are eligible for automatic delivery, including through the automatic agent handoff. Blank-description notes remain manual-only. Explicit note commands are unchanged.

Descriptions are routing criteria, not answer bodies. Write a short, precise scope such as “How to install nightly builds from official releases,” including important exclusions if needed. Do not give joke, irrelevant or media-only notes support-routing descriptions. Back up the database before backfilling existing descriptions; this change does not edit or seed production notes. Review descriptions and the actual note contents before enabling routing.

Each note gets an opaque request-local option ID (`n0`, `n1`, ...). Names such as `agent` or `none` cannot collide with router actions. Additions, deletions and renames appear in the next request. Immediately before delivery the router rechecks the selected name and description and loads the current note locally. A removed, renamed, newly manual-only or description-changed selection is dropped; it is never replaced with generated prose or stale note text.

## Bounds, privacy and failures

- Decisions gets text only: up to 4,000 UTF-8 bytes of current text, a visible referenced bot parent (up to 1,500 bytes), and at most four relevant retained user/assistant turns (1,000 bytes each) for bot replies. Earlier conversation summaries, message author profiles/IDs, guild emoji catalogs, tool reasoning and persistent memories are not included. The existing support agent retains its existing context/privacy rules.
- First release is **text-only**. Attachments are neither fetched nor classified, their filenames are not evidence, attachment-only messages are ignored, and images are removed from automatic agent handoff messages. Image support would require bounded base64 data URLs under the Decisions multimodal contract, not remote attachment URLs.
- Provider preferences always include `data_collection: "deny"` and `sort: "latency"`. No extra session/user tracking identifiers are sent to Decisions. The selected provider still receives the bounded message text; do not interpret this setting as local inference.
- Requests and responses are capped at 64 KiB. Catalogs over 128 eligible notes, names over 256 bytes or descriptions over 640 bytes fail closed rather than classifying a partial catalog. Message fields are UTF-8-safe truncated at their bounds.
- Two independent Decisions slots do not consume the three general-AI slots. Total classification timeout is four seconds, including at most one retry, a 100 ms pause and key failover. Each attempt has a two-second timeout. Retryable failures are transport/read failures, HTTP 408/429/5xx, and 401/402/403 when another configured key exists. Other HTTP failures and malformed responses are not retried. Redirects are refused.
- Missing/wrongly typed answers, unknown IDs, absent/null/nonfinite/out-of-range probabilities, non-normalized distributions (outside 0.01 rounding tolerance), a chosen option that is not a probability winner, and malformed/oversized envelopes are rejected. Provider `confidence` is not substituted for the selected option's probability.
- A bounded in-flight/seen message map suppresses concurrent duplicates and retains completed decisions for ten minutes. A separate per-user/same-note reservation prevents concurrent duplicate notes; its ten-minute cooldown starts only after a successful send. Failed classification/delivery releases its reservation so a later event can retry. Expired entries are pruned on activity; maps hold at most 1,024 entries and refuse new work when full rather than evicting active requests. State is in memory and resets on restart.
- Deletion events cancel registered requests before audit-log filtering; a bounded ten-minute tombstone map and the existing context-reset visibility guard suppress late results. Delivery rechecks visibility under the existing reply lock. A send already in progress cannot be recalled by cancellation.
- Decision logs contain the intended action, opaque Discord message ID, selected note name, answer/route probabilities, margin, elapsed classification time, resolved model and reported cost when available. Intended action is not a delivery counter: cooldown or visibility checks can still suppress it. Failure warnings omit upstream error strings. No message bodies, profiles, credentials, upstream error bodies or request URLs are logged by the Decisions client/router.

## Verification and rollout

Offline checks:

```sh
go test ./...
go test -race ./discord ./cmd ./config
go vet ./...
```

`discord/testdata/support-routing.json` contains **synthetic** labeled help requests and nonrequests, including realistic short follow-ups and support chatter. Tests inject synthetic probabilities and use local HTTP servers: they verify wire contracts, validation, routing policy, exact note delivery, context continuation, dynamic catalog handling, deduplication, cooldown, cancellation and fail-closed behavior. They make no model API calls and **do not measure classifier accuracy**.

Before production enablement, privately replay a representative, manually labeled sample against the actual request instructions and current note descriptions. Include chatter, staff advice, donation proof, human-directed replies, bot follow-ups and ambiguous requests, not just easy questions. Check false replies, selected-note correctness, abstentions, tail latency and reported `usage.cost`; inspect both Noul and Choice results. Keep real HAR exports, selected message text, API keys and replay outputs outside git and ordinary logs. Recalibrate after changing models, instructions, descriptions or thresholds. The implementation worker does not deploy or perform this paid/private replay.

API contract references:

- [Decisions request/response schema](https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-request.md)
- [Independent Noul and Choice questions](https://openrouter.ai/docs/guides/community/jev-tutorial.md)
- [Multimodal data-URL requirements](https://openrouter.ai/docs/guides/community/multimodal-decisions.md)
