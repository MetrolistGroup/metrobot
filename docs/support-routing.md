# Support routing

## Configuration

Set `openrouter_api_keys` and opt into Decisions routing with:

```json
{"openrouter_decision_model": "perplexity/pplx-decider-v1.2"}
```

An empty decision model keeps the legacy chat-model triage. Clear the setting and restart to restore that path. The setting adds no database migration or persistent routing statistics.

## Request handling

Automatic routing handles clear help requests in the configured guild's app-support channel. It ignores bots, webhooks, commands, chatter, thanks, staff coordination, human-directed replies and requests requiring staff action. Replies to this bot and retained, same-user follow-ups can continue a question without using Discord's Reply button.

Decisions asks two independent questions: whether the message needs an answer, and which eligible note or agent should handle it.

| Decision | Requirement |
| --- | --- |
| Answer automatically | Request probability ≥ 0.80, positive route and available answering model |
| Preload a selected note | Note probability ≥ 0.85 and lead over the next option ≥ 0.20 |
| Send a generated answer | Grounding probability ≥ 0.80 and no named defect |
| Offer More from this note | Independent note-use probability ≥ 0.95 |

A selected note is reference material, not permission to post its full body. The agent reads it through `get_note` and writes a concise answer. Uncertain note matches go to the agent. Only delivered answers enter assistant history; rejected drafts do not.

With Decisions enabled, casual requests needing Metrolist facts use the same isolated support path. Their classifier runs before compaction and favors support at probability 0.20. Ordinary chat keeps its own prompt. Classification errors do not fall back to unchecked app guidance.

## Evidence and practical answers

Support does not inherit the casual persona, stored base-prompt override or shared memory. Its tools are limited to approved notes, support guides and the official public repository.

Seven guides cover RPC, login, scrobbling, playlists, discovery, playback and casting. They load on demand through `load_support_guide`; one guide never hides the others. The checker receives the trusted core/project reference and the guides actually retrieved, leaving room for the answer's source evidence.

The agent should give a supported next step before asking another question. Playback guidance distinguishes low-risk checks from a diagnosis. Casting guidance scopes Google Cast to the standalone GMS build and asks which stage fails after giving the known setup path. Source review is not device testing; keep each guide's platform/build conditions.

Every generated support answer is checked against the conversation and this run's named tool results. Retained assistant claims and old tool provenance are not evidence. Defects such as `needs_research`, `unsupported`, `wrong_scope`, `unsafe` and `off_request` trigger a rewrite even when the grounding score is high. The agent gets up to two model rewrites; API failures are not permission to send an unchecked draft.

### Unhandled questions

When an accepted playback request fails generation or checking, or produces no answer, the bot can send general standalone-Android guidance from the current `.playback` note. If that note is missing, invalid or opted out, it tries `.nightly`. Both must have nonblank routing descriptions. The fallback requires playback wording in the latest message and excludes KMP; other topics and missing notes get the neutral unavailable notice, not unrelated playback advice. Explicit "hi", "hello" and "hey" greetings bypass the model.

Fallback notes are read at delivery time, require no extra model call and are labeled as general guidance, not a confirmed fix. Long content is attached rather than truncated. The question stays pending for a follow-up; the fallback does not start ambient replies or become an accepted diagnosis.

Intentional silence, staff-only requests, cancellations, deletion guards, permissions and rate limits still apply. A router outage cannot classify a new message as a support request. Explicit non-help outages retain the neutral error notice.

### Note access

Only notes with a nonblank `short_desc` enter automatic routing. Descriptions explain when a note applies, including exclusions; they are not answer bodies. Joke/media notes should remain manual-only. The catalog is rebuilt per request, using opaque IDs so note names cannot collide with router actions.

The agent and checker use the retrieved full note snapshot, including text beyond the usual tool limit. Later edits cannot change that evidence. Full `get_note` results are not shortened for checking; oversized requests fail closed.

More from this note opens the current full note privately. It appears only when that note supplied relevant facts, not merely because it was fetched. Deleted, renamed or opted-out notes become unavailable. Discord component rejection falls back to the same concise answer, not a full-note dump. Explicit note commands retain their normal layouts and visibility.

## GitHub research

- Issue search returns titles, body snippets and comment counts. Read relevant issues before treating them as answers. The issue tool includes the newest eight comments and author associations; OWNER, MEMBER and COLLABORATOR comments are maintainer statements. Other comments remain user reports.
- Code search and file excerpts establish labels, menus and conditions visible in that source, not released availability. Code search requires the configured GitHub token. File excerpts include numbered lines around up to four matches.
- Support searches may use short symptom/feature synonyms against the fixed public repository. Sensitive-text filters still apply. Other chat retains the current-request/public-result provenance rule; private profiles or channel history cannot authorize a search.
- Each issue/code research tool has a three-call budget. Support starts with eight tool rounds and a 90-second deadline, reserving a few additional rounds after a rejected draft. A research failure is returned as a tool error so another source can still help.

### Release and nightly claims

`get_metrolist_status` gives current release metadata and five tip commits, not a changelog. `get_metrolist_changes` compares the latest stable release with main or a published nightly. It scans up to 300 commits, returns at most ten literal-keyword matches and reports coverage limits. Filtering is local; empty partial results do not prove no fix exists.

Nightly comparisons resolve the tag, check its immutable build SHA and require an uploaded APK. Main-only commits do not prove nightly inclusion. `get_metrolist_commit` reads a public-sourced full SHA and up to five patches. Both tool payloads stay within 10 KiB; comparison HTTP responses have a separate 2 MiB limit.

A matching inspected change can support a backed-up, in-place nightly trial with an instability warning. A dependency bump alone does not prove which errors it fixes. Never present a generic Source error as a known diagnosis, promise success, or infer fixes from issue closure. Generic nightly installation guidance from the approved note makes no claim about a specific fix.

## Privacy and limits

- Automatic routing receives up to 4,000 bytes of current text, a visible bot parent up to 1,500 bytes, and four retained turns up to 1,000 bytes each. It excludes profiles, IDs, summaries, reasoning and shared memory.
- Casual app-guidance classification can also use 20 prior turns and a 2,000-byte summary. Support generation/compaction gets timestamps rather than Discord profiles.
- Automatic attachment-only requests are ignored. Images are removed from automatic handoff; filenames do not establish their contents. Ask for exact error text when an attachment was not read.
- Decisions bodies are capped at 64 KiB. The catalog allows 128 eligible notes, 256-byte names and 640-byte descriptions. Invalid IDs, probabilities, distributions, types or envelopes fail closed.
- Decisions has two slots, separate from the three answering-model slots. Its eight-second deadline includes four-second attempts, at most one retry, a 100 ms pause and bounded key failover. Redirects are refused.
- Provider preferences deny data collection, require supported parameters and sort by latency. Providers still receive the selected text: this is not local inference. No fixed cross-user session ID is sent.
- Seen/in-flight maps hold at most 1,024 entries. Completed events are deduplicated for ten minutes; failed delivery can retry after normal limits. Deletions cancel affected requests and retain ten-minute tombstones. A send already in progress cannot be recalled.
- Conversations expire after 24 hours and disappear on restart. Pending questions retain at most 40 source origins; overflowing that limit drops the corresponding history. No timers send unsolicited follow-ups.
- Routing logs record decisions, opaque message IDs, note names, probabilities, latency, model and reported cost, not message bodies or upstream error bodies.

## Chat memory and prompt settings

Ordinary chat can use the SQLite `garmin_base_system_prompt` override. Without it, the built-in prompt applies. `#general` always uses its short built-in prompt; support, triage and summarization use their own instructions. New requests read current settings; running requests keep their original prompt. Read failures stop the request.

There is no prompt editor command or web UI. The storage API provides `GetGarminSystemPrompt`, `SetGarminSystemPrompt` and `ResetGarminSystemPrompt`. Replacements must be nonblank UTF-8, at most 64 KiB. Writes are atomic and preserve text; inference trims outer whitespace. Reset deletes the override. Prompt text cannot grant permissions or change models/credentials, and is sent to the provider: keep secrets out.

Admins can use `/memory view`; append/replace/clear also require Nyx or Lamp. Agent writes require an explicit authorized owner request. Shared memory is lower-priority background data, capped at 16 KiB including its trailing newline. Summaries cannot write to it. Preserve the live file during upgrades; the repository copy is only a seed.

Conversations and summaries stay in bounded RAM context, expiring after 24 hours. Ambient continuation lasts two minutes where enabled. Restarts clear them, and context reset hides earlier channel messages and cancels affected requests. Compaction is lossy; older summaries do not have complete deletion lineage or permission provenance.

## Maintaining notes and guides

`docs/support-notes.json` is an authored update set, not an automatic seed or live export. Compare it with a fresh snapshot, back up the database, run `go test ./cmd -run TestAuthoredSupportNotesAreValid`, then apply reviewed row changes in one transaction. Never replace the live database with a development copy.

Keep each guide's pinned source revision, build/platform scope and conditions. Source review does not prove device compatibility, public release availability or tester access. Distinguish standalone FOSS/GMS/Izzy from KMP. Do not change staff access rules, credentials or production models while editing guidance. Keep private snapshots and receipts outside Git.

## Confirmed fixes

When the router stays silent on a follow-up, a separate check can recognize the original asker's confirmation that a delivered answer solved the problem. At probability 0.90, it adds the dated question/answer to `confirmed-fixes`, newest first, capped at 10 KiB with exact duplicates skipped. Plain thanks, bystander claims and different solutions do not qualify. Staff can edit or delete that note normally.

## Tests and rollout

```sh
go test ./...
go test -race ./...
go vet ./...
```

Offline tests use synthetic inputs, mocked HTTP and temporary databases. They check contracts and policy, not model accuracy.

Paid, non-posting replays:

- `TestSupportAgentLive`: answering and evidence checks, with named subtests for setup, corrections and safety cases.
- `TestSupportPlaybackAndCastingLive`: reported Source error, streamed-only follow-up and Wi-Fi casting cases.
- `TestSupportUpdateMessageLive` and `TestSupportSlowDownloadMessageLive`: full handler replays. The latter also checks inspected patch/build/link provenance.

Set `METROBOT_SUPPORT_LIVE_CONFIG`, `METROBOT_SUPPORT_LIVE_NOTES` and `METROBOT_SUPPORT_LIVE_REPORT`. Config needs the chat and Decisions settings; notes are JSON rows containing `name`, `short_desc` and `content`. Keep snapshots, drafts and reports private. These tests use a temporary database, read-only GitHub requests and mocked Discord delivery.

`TestSupportDecisionsLive` separately evaluates routing. Set `METROBOT_DECISIONS_LIVE_CONFIG`, `_NOTES`, `_CASES` and `_REPORT`; `_MODEL` optionally overrides the decision model. Cases contain `id`, `message`, optional `parent`/`conversation` and an `expected` array. Synthetic fixtures live in `discord/testdata/support-decisions-eval.json`.

Review delivered answers manually and repeat important cases after changing models, prompts, guides or notes. Do not tune labels to make a replay pass. Small samples and high evidence scores are not accuracy guarantees. Back up production data before deployment; preserve live notes, config and memory.

API references: [Decisions schema](https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-request.md), [independent questions](https://openrouter.ai/docs/guides/community/jev-tutorial.md), [multimodal contract](https://openrouter.ai/docs/guides/community/multimodal-decisions.md).
