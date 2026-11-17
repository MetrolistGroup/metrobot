# Support routing

## Configuration

Set `openrouter_api_keys`, use a dedicated Decider for initial support triage and evidence checking, and GLM 5.3 Flash for the actual responses:

```json
{"openrouter_model": "z-ai/glm-5.3-flash:floor", "openrouter_decision_model": "perplexity/pplx-decider-flash-v1.2"}
```

Decider first determines whether a message needs support and supplies the matching note, if any. Only accepted requests reach the chat model. Generated drafts are checked before delivery. A failed automatic request stays silent; a failed explicit request gets a short honest reply. Ordinary chat uses GLM directly.

Optional Qwen decisions use `openrouter_decision_model: "qwen/qwen3.7-flash"`; the chat model stays independent. In that mode, Qwen judgments use chat completions with a validated tool schema, not the Decider-only API. Routing has reasoning disabled; evidence checks request a 512-token reasoning budget and return a concrete finding for rejected drafts. Routing choice weights are normalized before applying the existing thresholds; model scores are not calibrated accuracy estimates. Qwen evidence checking makes one defect decision: `none` maps to acceptance, any named defect to rejection. Its logged grounding value is therefore 1 or 0, not a confidence estimate. This avoids a contradictory second score rejecting an answer while naming no defect; Decider retains its probability gate. Invalid, missing or truncated verdicts fail closed after at most one retry. Qwen endpoints reject `tool_choice=required`, so both Qwen decision paths validate returned calls rather than requiring that unsupported parameter.

An empty decision model uses legacy chat triage **without an independent answer check**. Existing configurations are not changed automatically. No database migration or persistent routing statistics are added.

## Request handling

Explicit Discord chat accepts case-insensitive `garmin <prompt>` with any prompt, without brackets or a required comma. `garmin, <prompt>`, conversational aliases, bot mentions and direct replies remain supported. Telegram has no AI chat handler.

Automatic routing handles clear help requests in the configured guild's app-support channel. It ignores bots, webhooks, commands, chatter, thanks, staff coordination, human-directed replies and requests requiring staff action. Direct replies to this bot are explicit requests, like the call word: they skip the router and go to the support agent, which can still choose silence.

Every delivered app-support reply, including the fallback reply, opens a support session for that asker. For three minutes after the asker's latest message, their messages go straight to the chat model as explicit requests, without the router. Each message extends the window. Messages that mention or reply to someone else, commands and other users' messages still use the router. The session ends when the window passes or when the model deliberately stays silent or reacts, for example after "that worked, thanks"; that ending also runs the confirmed-fix check. Sessions live in memory, are capped at 1,024 entries and clear on restart. Retained, same-user follow-ups can still continue a question without using Discord's Reply button.

Decisions asks two independent questions: whether the message needs an answer, and which eligible note or agent should handle it.

| Decision | Requirement |
| --- | --- |
| Answer automatically | Request probability ≥ 0.80 and available answering model. A `none` route at that probability still goes to the agent, which can choose silence. |
| Preload a selected note | Note probability ≥ 0.85 and lead over the next option ≥ 0.20 |
| Send a generated answer | No named defect and grounding probability ≥ 0.50 |
| Offer More from this note | Independent note-use probability ≥ 0.95 |

A selected note is not permission to dump its full body. With Decider configured, the chat model generates the response from the retrieved reference. In Qwen-only decision mode, even a tentative top-ranked note is loaded as reference material instead of discarding it when its normalized routing weight misses the threshold. This does not authorize a reply: after `get_note` the separate applicability selector must choose one complete authored section at the same 0.85/0.20 thresholds. The runtime sends that section verbatim, including its heading, preamble and warnings, rather than asking Qwen to paraphrase it. Markdown code examples do not create sections. Unmatched, uncertain, multi-section or live-data questions continue through the agent and evidence checker. A Qwen draft judged to use a note must match an authored section (or the whole retrieved note) exactly: the checker cannot approve added claims by calling them a faithful paraphrase. Complex note synthesis can therefore abstain instead of inventing an answer. Only delivered answers enter assistant history; rejected drafts do not.

Routing and evidence checks run only in app-support. Every other channel uses the chat model directly, including Metrolist questions and follow-ups, with no Decider classification, evidence check or confirmed-fix learning call. Ordinary chat keeps its own prompt, project reference and read-only tools.

## Evidence and practical answers

Support does not inherit the casual persona, stored base-prompt override or shared memory. Its tools are limited to approved notes, support guides and the official public repository.

Seven guides cover RPC, login, scrobbling, playlists, discovery, playback and casting. They load on demand through `load_support_guide`; one guide never hides the others. The checker receives the trusted core/project reference and the guides actually retrieved, leaving room for the answer's source evidence.

The agent should give a supported next step before asking another question. Playback guidance distinguishes low-risk checks from a diagnosis. Casting guidance covers Google Cast, F-Cast, SONOS and DLNA on Android and desktop. Google Cast is built into the universal Android APK. Ask which receiver/protocol and stage fails. Source review is not device testing; keep each guide's platform/build conditions.

Exact note sections need a positive applicability selection, but no free-form generation or second paraphrase check. With a decision model configured, every generated support answer is checked against the conversation and this run's named tool results. URLs and Discord channel references must also match supplied source text; a high model score cannot approve an invented destination. Retained assistant claims and old tool provenance are not evidence. Defects such as `needs_research`, `unsupported`, `wrong_scope`, `unsafe` and `off_request` trigger a rewrite even when the grounding score is high. The agent gets one model rewrite with the specific finding. A rejection does not automatically force more research: an ambiguous question may need clarification, and a loaded note may already answer it. API failures are not permission to send an unchecked draft.

### Unhandled questions

Failed automatic app-support replies and failed replies in other channels are silent. An explicit app-support request (call word, mention or reply to the bot) that ends with a provider/checker/tool error, timeout, exhausted verification retries, an empty response or missing required sources gets one fixed reply asking for the app version, platform and exact error text, and pointing to staff. It contains no generated claims and is not stored as an answer, so the pending question carries into the follow-up. Intentional `do_not_respond` silence still sends nothing. Failed Discord delivery is logged, not retried as an unreferenced message or another format. Bounded provider retries and evidence-driven rewrites can still recover before a successful checked answer is delivered; failed tool execution stops the attempt.

Accepted support requests retain their pending user question without storing rejected drafts or failure messages, so a later follow-up can continue. Completed failed attempts are deduplicated, and no failure activates ambient replies. Intentional silence, staff-only requests, cancellation/deletion guards and permissions still apply. A router outage cannot classify a new message as a support request. Explicit "hi", "hello" and "hey" greetings still bypass the model.

### Note access

Only notes with a nonblank `short_desc` enter automatic routing. Descriptions explain when a note applies, including exclusions; they are not answer bodies. Joke/media notes should remain manual-only. The catalog is rebuilt per request, using opaque IDs so note names cannot collide with router actions.

The agent and checker use the retrieved full note snapshot, including text beyond the usual tool limit. Later edits cannot change that evidence. Full `get_note` results are not shortened for checking; oversized requests fail closed.

More from this note opens the current full note privately. For generated answers it appears only when that note supplied relevant facts, not merely because it was fetched. A selected verbatim section uses its retrieved note directly. Deleted, renamed or opted-out notes become unavailable. Discord component rejection sends no fallback message. Explicit note commands retain their normal layouts and visibility.

## GitHub research

- Issue search returns titles, body snippets and comment counts. Read relevant issues before treating them as answers. The issue tool includes the newest eight comments and author associations; OWNER, MEMBER and COLLABORATOR comments are maintainer statements. Other comments remain user reports.
- Code search and file excerpts establish labels, menus and conditions visible in that source, not released availability. Code search requires the configured GitHub token. File excerpts include numbered lines around up to four matches.
- Support searches may use short symptom/feature synonyms against the fixed public repository. Sensitive-text filters still apply. Other chat retains the current-request/public-result provenance rule; private profiles or channel history cannot authorize a search.
- Each issue/code research tool has a three-call budget. Support starts with four tool rounds and a 90-second deadline. A rejected draft allows one rewrite, with at most two additional rounds for targeted retrieval and the final answer. A final tool-only response can receive one tool-free nudge. A failed research lookup stops the attempt silently instead of generating a lookup-failure message.

### Release and nightly claims

`get_metrolist_status` gives current release metadata and five tip commits, not a changelog. `get_metrolist_changes` compares the latest stable release with main or a published nightly. It scans up to 300 commits, returns at most ten literal-keyword matches and reports coverage limits. Filtering is local; empty partial results do not prove no fix exists.

Nightly comparisons resolve the tag, check its immutable build SHA and require an uploaded APK. Main-only commits do not prove nightly inclusion. `get_metrolist_commit` reads a public-sourced full SHA and up to five patches. Both tool payloads stay within 10 KiB; comparison HTTP responses have a separate 2 MiB limit.

A matching inspected change can support a backed-up, in-place nightly trial with an instability warning. A dependency bump alone does not prove which errors it fixes. Never present a generic Source error as a known diagnosis, promise success, or infer fixes from issue closure. Generic nightly installation guidance from the approved note makes no claim about a specific fix.

## Privacy and limits

- Automatic routing receives up to 4,000 bytes of current text, a visible bot parent up to 1,500 bytes, and four retained turns up to 1,000 bytes each. It excludes profiles, IDs, summaries, reasoning and shared memory.
- Support generation gets timestamps rather than Discord profiles.
- Automatic attachment-only requests are ignored. Images are removed from automatic handoff; filenames do not establish their contents. Ask for exact error text when an attachment was not read.
- Decisions bodies are capped at 64 KiB. The catalog allows 128 eligible notes, 256-byte names and 640-byte descriptions. Invalid IDs, probabilities, distributions, types or envelopes fail closed.
- Decisions has two routing slots, separate from the three answering-model slots. Routing has a 15-second deadline, with seven-second Qwen attempts (four seconds for Decider), at most one retry, a 100 ms pause and bounded key failover. Qwen evidence checks allow up to 45 seconds with twenty-second attempts, still within the support request's 90-second deadline. Redirects are refused.
- Provider preferences deny data collection, require supported parameters and sort the default chat route by price. Decision routing keeps its existing provider preferences. Providers still receive the selected text: this is not local inference. No fixed cross-user session ID is sent.
- Seen/in-flight maps hold at most 1,024 entries. Completed events are deduplicated for ten minutes; failed delivery can retry after normal limits. Deletions cancel affected requests and retain ten-minute tombstones. A send already in progress cannot be recalled.
- Conversations keep up to 12 recent messages, starting with a user message, plus an omission note when older messages are dropped. Use `read_channel_history` for older context. They expire after 24 hours and disappear on restart. Pending questions retain at most 40 source origins; overflowing that limit drops the corresponding history. Images remain in only the last four messages, capped at eight images per request. No timers send unsolicited follow-ups.
- Routing logs record decisions, opaque message IDs, note names, probabilities, latency, model and reported cost, not message bodies or upstream error bodies.

## Short replies and embeds

The default chat model is `z-ai/glm-5.3-flash:floor`, with low reasoning effort. Chat and support output caps are 4,096 tokens: brevity comes from the prompts, and the cap only stops runaway output while leaving room for GLM reasoning. OCR keeps its 768-token cap. Fallback chat-model triage has a 128-token output cap. GLM requires reasoning, so tool follow-ups, final-answer retries and OCR also use low effort rather than the rejected `reasoning.enabled=false` setting. Other models still honor explicit reasoning-free requests. The `:floor` route sorts eligible providers by price. The Decider uses its structured Decisions API, not chat completion token settings. Optional Qwen decisions retain their separate explicit budgets. Static instructions and the short project reference precede dynamic Discord context and shared memory. The full package/feature reference and setup guides load on demand. Later tool calls reference an earlier identical large result instead of sending another copy; the checker retains original evidence.

`TestGarminPromptBudget` counts prompt bytes with empty shared memory. It estimates tokens at four bytes per token; provider tokenizers and cache billing vary.

| Text input | Before | After |
| --- | ---: | ---: |
| Chat prompt bytes | 16,566 | 7,406 |
| Support prompt bytes | 14,078 | 8,711 |
| Chat generation, estimated tokens | 5,342 | 2,332 |
| Support generation, estimated tokens | 4,720 | 2,658 |

The generation examples include 160 bytes per retained message, with the old 30-message window and new 12-message window. They exclude tool schemas, retrieved evidence, runtime context, images and routing/checker calls. Direct chat still needs one generation call. Generated support still needs its applicability/routing and evidence checks; call cost depends on the actual note, research and retry path. Positively selected verbatim sections need no generation or second evidence check. The full package/feature reference is absent from the always-sent prefix and remains available through `load_skill`.

Ordinary generated support replies target 20–30 words in 1–2 short sentences: a direct answer with one or two useful next steps, or one necessary question. Use everyday English, without feature lectures, research recaps, repeated symptoms or diagnostic questionnaires. Omit commit hashes, issue numbers and implementation details unless requested. Length is a prompt instruction only; no word-count gate rejects a longer draft. Explicit detail requests and authored/verbatim note delivery retain their full scope. Keep requested links, platform conditions and safety warnings. Other replies ask for plain, short answers. Essays, full code dumps and huge lists get a brief refusal or a summary offer. Casual profanity is allowed; slurs and harassment are not. Generated Discord AI replies reuse the 1900-byte clipper, preserve UTF-8 and prefer a nearby word boundary. They stay in one message. Verbatim support-note attachments and private note access retain their existing behavior.

For an embed, the model can append one trailing block:

````text
here is the release.
```embed
{"title":"Release","description":"Brief details","url":"https://metrolist.cc","fields":[{"name":"Status","value":"Available","inline":true}],"color":5793266}
```
````

Any AI reply, including app-support, can also use the staff-note `components-v2` layout (text, separator, section, gallery and link buttons; see [note markup](notes.md#components-v2)). Text around the block is shown around it. The block is validated with the note parser, is never clipped, and falls back to its readable text if invalid or rejected by Discord. FAQ blocks stay text because selectors need a saved note. In app-support the evidence checker reads the readable version, so button and media URLs must come from the evidence. A layout answer that uses a note keeps the More from this note button inside its container.

The parser accepts only title, description, URL, fields and color. It validates Discord's per-field and total limits using UTF-16 lengths, checks HTTP/HTTPS URLs and 24-bit colors, and falls back to text for invalid embeds. Malformed JSON stays plain text and is subject to reply clipping. The bot disables mentions and stores the delivered embed text in conversation history. Telegram's public reply helper renders the same format as plain text. Telegram has no AI chat handler.

## Chat memory and prompt settings

Ordinary chat can use the SQLite `garmin_base_system_prompt` override. Without it, the built-in prompt applies. `#general` always uses its short built-in prompt; support, triage and summarization use their own instructions. New requests read current settings; running requests keep their original prompt. Read failures stop the request.

There is no prompt editor command or web UI. The storage API provides `GetGarminSystemPrompt`, `SetGarminSystemPrompt` and `ResetGarminSystemPrompt`. Replacements must be nonblank UTF-8, at most 64 KiB. Writes are atomic and preserve text; inference trims outer whitespace. Reset deletes the override. Prompt text cannot grant permissions or change models/credentials, and is sent to the provider: keep secrets out.

Admins can use `/memory view`; append/replace/clear also require Nyx or Lamp. Agent writes require an explicit authorized owner request. Shared memory is lower-priority background data, capped at 16 KiB including its trailing newline. Summaries cannot write to it. Preserve the live file during upgrades; the repository copy is only a seed.

Conversations and summaries stay in bounded RAM context, expiring after 24 hours. Ambient continuation lasts two minutes where enabled. Restarts clear them, and context reset hides earlier channel messages and cancels affected requests. Compaction is lossy; older summaries do not have complete deletion lineage or permission provenance.

## Maintaining notes and guides

`docs/support-notes.json` contains 15 repository-managed notes. Startup embeds this set and known earlier repository versions. The transactional `metrolist_v14_notes_v1` migration updates an existing note only when its content and description match a known earlier version. It accepts the exact FAQ wrapping produced by `note_markup_v1`. It never inserts absent notes, deletes aliases or overwrites unknown/staff-edited notes. Logs identify updated, absent, already-current and manual-review rows. See [the note migration](notes.md#metrolist-v14-note-migration). Never replace a live database with a development copy.

Metrolist v14 is the public KMP rewrite in MetrolistGroup/Metrolist, with no donor/tester/source gate. Keep `kmp` for platform and migration questions, `ver` for the single package answer and `playbackkmp` as a compatibility alias of `playback`. New donors get a thank-you or the optional staff-authored `donorwelcome` note; the bot no longer loads `testertour` on donor-role changes. Existing legacy KMP FAQ selector IDs still resolve the current `kmp` FAQ and carry no access policy.

Keep each guide's version/platform conditions. Historical Android 13.x and pre-v14 rewrite menus may differ in v14; do not turn them into verified v14 paths. The `download` cache location and `uploads` destination came from Android 13.x source review. Keep unknown v14 behavior explicit rather than inventing menus or diagnoses. Update both the authored notes and their embedded migration copy, then run `go test ./cmd -run TestAuthoredSupportNotesAreValid` and the migration tests. Leave staff access rules and credentials alone. Models remain a separate configuration choice.

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

- `TestSupportAgentLive`: answering and evidence checks, including uploaded-album metadata, downloaded files, donation URLs, fork-access scope, setup and safety cases.
- `TestSupportNoteMessagesLive`: real routing-to-delivery checks for uploaded metadata, fork/source access and bug reports, with Discord delivery mocked.
- `TestSupportEvidenceLive`: fixed good/bad answers using the configured decision model to check that useful clarifications pass and invented fixes, payment claims and unsafe steps are rejected.
- `TestSupportPlaybackAndCastingLive`: reported Source error, streamed-only follow-up and Wi-Fi casting cases.
- `TestSupportUpdateMessageLive` and `TestSupportSlowDownloadMessageLive`: full handler replays. The latter also checks inspected patch/build/link provenance.

Set `METROBOT_SUPPORT_LIVE_CONFIG`, `METROBOT_SUPPORT_LIVE_NOTES` and `METROBOT_SUPPORT_LIVE_REPORT`. Config needs the chat and decision settings; set both to Qwen3.7 Flash for Qwen-only checks. `TestSupportAgentLive` also accepts an empty decision model to inspect the legacy unchecked path. Notes are JSON rows containing `name`, `short_desc` and `content`. Keep snapshots, drafts and reports private. These tests use a temporary database, read-only GitHub requests and mocked Discord delivery.

`TestSupportDecisionsLive` separately evaluates routing. Set `METROBOT_DECISIONS_LIVE_CONFIG`, `_NOTES`, `_CASES` and `_REPORT`; `_MODEL` optionally overrides the decision model. Cases contain `id`, `message`, optional `parent`/`conversation` and an `expected` array. Synthetic fixtures live in `discord/testdata/support-decisions-eval.json`.

Review delivered answers manually and repeat important cases after changing models, prompts, guides or notes. Do not tune labels to make a replay pass. Small samples and high evidence scores are not accuracy guarantees. Back up production data before deployment; preserve live notes, config and memory.

API references: [Decisions schema](https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-request.md), [independent questions](https://openrouter.ai/docs/guides/community/jev-tutorial.md), [multimodal contract](https://openrouter.ai/docs/guides/community/multimodal-decisions.md).
