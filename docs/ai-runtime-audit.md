# AI runtime audit

This records the offline audit before the later support rollout. The audit itself changed no production data or configuration. Current support routing, casual app-guidance checks and shared-memory rules are documented in [support-routing.md](support-routing.md) and [prompt.md](prompt.md). The limits below describe the audited version, not the current deployment.

## Paths covered

| Path | Implementation |
| --- | --- |
| Explicit chat, retained replies and ambient chat | `discord/garmin.go`, `discord/garmin_agent.go` |
| App-support handoff and legacy triage | `discord/garmin_support.go`, `discord/support_decisions.go` |
| Support answer and note-use checks | `discord/support_answer.go` |
| Conversation compaction | `discord/garmin.go` |
| Chat completions, provider routing and retries | `cmd/ai.go`, `cmd/openrouter.go` |
| Decisions wire contract, bounds and retries | `cmd/decisions.go` |
| Public lookup clients and caches | `github/`, `firecrawl/`, `gsmarena/`, `nanoreview/`, `technicalcity/`, `db/gsmarena.go` |
| Shared-memory commands and authorization | `discord/handlers.go`, `discord/garmin_agent.go`, `cmd/garmin_memory.go` |

There is no separate Telegram model-generation path in the audited code.

## Changes

### Conversation retention

Conversation text stays in RAM. The 24-hour expiry, two-minute ambient window, bounded maps, restart clearing and explicit resets remain intentional. No disk conversation store was added.

Fixes preserve the oldest batch until compaction, keep complete recent exchanges, validate summaries, copy snapshots instead of sharing mutable slices, prevent older concurrent completions from replacing newer context, scope references to their channel/guild and cancel requests whose direct parents are deleted. Failed compaction keeps bounded recent text and explicitly marks omitted earlier context rather than presenting a fabricated summary.

Compaction no longer learns global project facts. Existing shared memory is preserved. Agent writes require an explicit owner request; slash-command append/replace/clear require Nyx or Lamp and retain their existing admin requirement. Admins can still view memory.

### Provider and tool-data caches

Provider prompt caching is not conversation storage. The built-in public system prefix is separated from appended memory and volatile request context. Explicit cache hints apply only to documented supporting models, not automatically to custom prompts or private appended data. The default Qwen model has no documented explicit-cache hint in the current allowlist. A fixed cross-user session ID is no longer sent. Provider-managed automatic caching may still apply; offline tests do not establish hit rates or savings.

Request copying preserves caller data. Provider handling validates envelopes, bounds bodies, refuses unsafe redirects and retains cancellation, key failover and bounded retry/backoff behavior. Privacy preferences and model defaults remain unchanged.

Hardware caches retain their existing one-day search/index and seven-day detail TTLs. Persisted GSMArena index entries no longer receive a fresh in-memory TTL. Corrupt or unavailable caches fall back to the source; failed cache writes do not discard successfully fetched data. GitHub action/release cache results are copied before returning. Public repository checks, scoped issue filtering, response limits and cancellation remain enforced. Filtered issue pages do not invent repository-wide totals. GSMArena proxy request contexts remain alive until the response body is closed.

### Tool and skill discovery

General chat offers configured, authorized capabilities without keyword-based tool or skill loading. `load_skill` and `load_support_guide` remain callable across rounds; loading one skill or using the calculator does not hide unrelated tools. The small support core remains always present in app-support, while full guides are retrieved on demand. The support evidence checker retains all trusted guides and its existing thresholds.

Availability does not mandate unrelated calls. Explicit source requirements remain separate, heuristic checks. App-support keeps its restricted tool set; missing services, owner consent, Discord permissions, resets and visibility are still enforced.

Public queries are constrained to the current request or public results from this request, not retained private text. GitHub subjects require contiguous provenance rather than independently matching words. Required public lookups precede private history; external query tools close after history retrieval. Image/history links may be cited without becoming authorized public-query inputs. Device-page URLs must be present in successful device results, including when lookup was optional; host checks normalize DNS dots and IDNA.

## Verification and limits

Backend regression tests use synthetic inputs, local HTTP mocks and temporary databases. Run:

```sh
go test ./...
go test -race ./...
go vet ./...
git diff --check
```

Independent reviews cover the component patches and discovery/memory follow-ups. Reviewers in read-only sandboxes cannot run Go tests; parent verification is separate.

Remaining limits:

- No live API replay, provider cache-hit measurement, latency/cost benchmark, Discord UI test, hardware check or production rollout was performed. Broader tool schemas and deferred history can cost more tokens or model rounds.
- Tool discovery and source provenance do not establish semantic answer correctness. Device/source intent still uses heuristics; URL checks do not prove a page matches every claimed product or specification. Casual chat still lacks the support-only evidence gate. Previous live Qwen quality problems are not declared fixed.
- Retained chat does not preserve trusted public-tool provenance. A public lookup whose subject appears only in earlier chat can require the user to restate it, even though the conversation itself is still retained.
- Credential-shaped requests fail closed, including ambiguous benign password/Bearer/possessive-secret phrases. These patterns are not a complete secret detector. Users may need to restate a credential-free public subject.
- Restarts, expiry, resets and bounded eviction still clear context. Compaction is lossy. Older summaries do not track complete deletion lineage or original permission provenance; deleting an old message does not prove its facts were erased from every summary.
- Existing shared-memory claims were not audited or rewritten by this change. Stopping automatic learning does not correct old claims.

Further live validation requires separate authorization. Passing offline tests is not deployment approval.
