# Automatic app-support routing

`openrouter_decision_model` is optional and defaults to `""`. Empty keeps the existing 15-second chat-model handoff triage unchanged. To opt in, configure an OpenRouter Decisions model alongside `openrouter_api_keys`, for example:

```json
{
  "openrouter_decision_model": "perplexity/pplx-decider-v1.2"
}
```

This changes only unprompted messages in the configured guild's app-support channel. Explicit bot prompts, `.note`/`.notes` and slash commands retain their existing behavior. Clearing the setting and restarting rolls back to legacy triage. There are no new dependencies, tables, migrations or persistent routing statistics.

## Routing

The client posts to `https://openrouter.ai/api/alpha/decisions`, not chat completions. In one request it asks two independent questions:

- `should_answer` uses Noul to identify an information request or unresolved problem about Metrolist or Metrobot.
- `route` uses Choice to select an eligible saved note, `agent` or `none`.

Neither question assumes it can read the other's answer. Both exclude chatter, keyword-only mentions, reactions, thanks, advice to others, staff coordination, human-directed conversation, donation proof and staff-only role/access requests. There is no keyword-triggered auto-answer rule. Bots, webhooks, other guilds/channels, command prefixes (`.`, `!`, `/`), mentions of other users and replies to humans are filtered before classification. Replies to this bot are allowed, including known retained bot replies whose parent is unavailable in the event; unknown reply targets are ignored conservatively.

An automatic reply requires `should_answer >= 0.80` and a route other than `none`. Sending a note also requires a winning route probability of at least `0.85` and a margin of at least `0.20` over the next option. The bot sends the current note through the existing renderer, suppresses mentions, and remembers the delivered text for follow-ups.

A clear request with an uncertain note match goes to the existing constrained support agent. The `agent` route does the same, without another chat-model triage pass. The agent can use eligible notes, the support skill and verified completed official issues. Its errors and busy state stay silent. A low request probability, `none`, invalid responses and API errors also stay silent. API failures never trigger a chat-model fallback. There is no new routing menu. FAQ notes keep their existing selectors.

The thresholds were checked against labeled replays, not calibrated into accuracy guarantees. A high probability does not prove a diagnosis or fix. This change adds no crash diagnoses or data-clearing instructions.

## Note opt-in and changes

The catalog is rebuilt from `Notes.DB.ListNotes()` on every classification. Only notes with a nonblank `short_desc` are eligible for automatic delivery, including through the automatic agent handoff. Blank-description notes remain manual-only. Explicit note commands are unchanged.

Descriptions are routing criteria, not answer bodies. Write a short, precise scope such as "How to install nightly builds from official releases", including important exclusions if needed. Do not give joke, irrelevant or media-only notes support-routing descriptions. Back up the database before backfilling existing descriptions; this change does not edit or seed production notes. Review descriptions and the actual note contents before enabling routing.

Each note gets an opaque request-local option ID (`n0`, `n1`, ...). Names such as `agent` or `none` cannot collide with router actions. Additions, deletions and renames appear in the next request. Immediately before delivery the router rechecks the selected name and description and loads the current note locally. A removed, renamed, newly manual-only or description-changed selection is dropped; it is never replaced with generated prose or stale note text.

## Bounds, privacy and failures

- Decisions gets text only: up to 4,000 UTF-8 bytes of current text, a visible referenced bot parent (up to 1,500 bytes), and at most four relevant retained user/assistant turns (1,000 bytes each) for bot replies. Earlier conversation summaries, message author profiles/IDs, guild emoji catalogs, tool reasoning and persistent memories are not included. The existing support agent retains its existing context/privacy rules.
- This release accepts text only. Attachments are neither fetched nor classified, their filenames are not evidence, attachment-only messages are ignored, and images are removed from automatic agent handoff messages. Image support would require bounded base64 data URLs under the Decisions multimodal contract, not remote attachment URLs.
- Provider preferences always include `data_collection: "deny"` and `sort: "latency"`. No extra session/user tracking identifiers are sent to Decisions. The selected provider still receives the bounded message text; do not interpret this setting as local inference.
- Requests and responses are capped at 64 KiB. Catalogs over 128 eligible notes, names over 256 bytes or descriptions over 640 bytes fail closed rather than classifying a partial catalog. Message fields are UTF-8-safe truncated at their bounds.
- Two independent Decisions slots do not consume the three general-AI slots. Total classification timeout is four seconds, including at most one retry, a 100 ms pause and key failover. Each attempt has a two-second timeout. Retryable failures are transport/read failures, HTTP 408/429/5xx, and 401/402/403 when another configured key exists. Other HTTP failures and malformed responses are not retried. Redirects are refused.
- Missing/wrongly typed answers, unknown IDs, absent/null/nonfinite/out-of-range probabilities, non-normalized distributions (outside 0.01 rounding tolerance), a chosen option that is not a probability winner, and malformed/oversized envelopes are rejected. Provider `confidence` is not substituted for the selected option's probability.
- A bounded in-flight/seen message map suppresses concurrent duplicates and retains completed decisions for ten minutes. A separate per-user/same-note reservation prevents concurrent duplicate notes; its ten-minute cooldown starts only after a successful send. Failed classification/delivery, including busy or failed agent handoffs, releases the seen entry so a later event can retry after normal rate limits. Successful delivery and intentional agent silence remain deduplicated. Expired entries are pruned on activity; maps hold at most 1,024 entries and refuse new work when full rather than evicting active requests. State is in memory and resets on restart.
- Deletion events cancel registered requests before audit-log filtering; a bounded ten-minute tombstone map and the existing context-reset visibility guard suppress late results. Delivery rechecks visibility under the existing reply lock. A send already in progress cannot be recalled by cancellation.
- Decision logs contain the intended action, opaque Discord message ID, selected note name, answer/route probabilities, margin, elapsed classification time, resolved model and reported cost when available. Intended action is not a delivery counter: cooldown or visibility checks can still suppress it. Failure warnings omit upstream error strings. No message bodies, profiles, credentials, upstream error bodies or request URLs are logged by the Decisions client/router.

## Verification and rollout

Offline checks:

```sh
go test ./...
go test -race ./discord ./cmd ./config
go vet ./...
```

`discord/testdata/support-routing.json` contains synthetic labeled help requests and nonrequests, including short follow-ups and support chatter. Tests inject synthetic probabilities and use local HTTP servers: they verify wire contracts, validation, routing policy, exact note delivery, context continuation, dynamic catalog handling, deduplication, cooldown, cancellation and fail-closed behavior. They make no model API calls and do not measure classifier accuracy.

Before production enablement, privately replay a representative, manually labeled sample against the actual request instructions and current note descriptions. Include chatter, staff advice, donation proof, human-directed replies, bot follow-ups and ambiguous requests, not just easy questions. Check false replies, selected-note correctness, abstentions, tail latency and reported `usage.cost`; inspect both Noul and Choice results. Keep real HAR exports, selected message text, API keys and replay outputs outside git and ordinary logs. Recalibrate after changing models, instructions, descriptions or thresholds. `TestSupportDecisionsLive` is an opt-in paid replay. It uses a temporary database and never connects to Discord or opens the production database. Supply `METROBOT_DECISIONS_LIVE_CONFIG`, `METROBOT_DECISIONS_LIVE_NOTES`, `METROBOT_DECISIONS_LIVE_CASES` and `METROBOT_DECISIONS_LIVE_REPORT`. The config supplies OpenRouter keys and the model; `METROBOT_DECISIONS_LIVE_MODEL` can override the model for comparisons. Notes are a JSON array with `name`, `short_desc` and `content`. Cases have `id`, `message`, optional `parent`, and an `expected` array of acceptable actions or note names. Run `go test ./discord -run '^TestSupportDecisionsLive$' -v`. It reports every mismatch without changing labels or sending public replies. The synthetic evaluation cases are in `discord/testdata/support-decisions-eval.json`.

On 25 October 2026, the revised prompt on Decider Flash V1.2 matched 77 of 84 synthetic cases. Decider V1.2 matched 82 of 84 with the initial 0.90 request threshold. The latter took a median 292 ms and cost $0.00156260 for that run. With the request threshold set to 0.80, a separate 60-message HAR sample matched 54 labels: four requests stayed silent and two went to the agent instead of directly to a note. It sent no incorrect notes and routed no labeled chatter to the agent in that sample. Median latency was 292 ms, p95 was 549 ms, and the run cost $0.00112562. A final rerun at the 0.80 request threshold matched 83 of 84 synthetic labels. The remaining case went to the agent instead of directly to the update note, so it did not post a wrong note. Median latency was 296 ms, p95 was 378 ms, and the run cost $0.00156260. These samples do not establish channel-wide accuracy. Keep the conservative note threshold and recheck after changing the model or catalog.

API contract references:

- [Decisions request/response schema](https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-request.md)
- [Independent Noul and Choice questions](https://openrouter.ai/docs/guides/community/jev-tutorial.md)
- [Multimodal data-URL requirements](https://openrouter.ai/docs/guides/community/multimodal-decisions.md)
