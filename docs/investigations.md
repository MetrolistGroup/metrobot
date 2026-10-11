# Dashboard investigations

These are read-only, GET-only endpoints on the existing dashboard. Every request
requires a current Discord bot-admin session; revoked admins lose access on the
next request. Host and supplied Origin must match the configured dashboard.
Responses are JSON with `Cache-Control: no-store`. Errors use `{"error":"…"}`;
upstream errors and bot credentials are never returned. No migrations, indexes,
backfills, live configuration changes, or edits to existing case/log data are needed.

## Stored cases

`GET /api/cases?from=2026-03-29&to=2026-03-29&q=reason&before=123`

All parameters are optional. Without dates, this lists **all retained database
cases**, including Discord and Telegram, without a hidden total limit. `from` and
`to` are inclusive UTC calendar days, independently optional. Text is a literal
contains search across case number, platform, target/moderator IDs, action and
reason (SQLite case folding is ASCII). SQL wildcard characters are literal.

```json
{"items":[{"case_number":123,"platform":"discord","action_type":"warn","target_id":"1234","moderator_id":"5678","reason":"Example reason","timestamp":"2026-03-29T12:00:00Z"}],"next_cursor":"123"}
```

Results are newest case-number first, at most 50 per page. Pass `next_cursor` as
`before` with the same filters; an empty cursor means exhausted. No Discord API
connection is necessary for database cases. Missing/deleted database cases are
not recreated. Historic Discord case posts may instead be read through the
`moderation` log source below.

## Historical log search

`GET /api/logs?source=message&from=2026-03-29&to=2026-03-29&q=needle`

Both dates are required. `from=to` searches one exact UTC day, including midnight
at the start and excluding midnight of the following day. Dates must be within
Discord's representable snowflake range, starting 2015-01-01. `q` is optional,
case-insensitive literal text (no regex/wildcards). It searches body text,
embed titles/descriptions/fields/footer/author, nested component text, attachment
names/descriptions, and downloaded text attachment **content**, even when the
message body does not match.

Sources are server-controlled; clients cannot select guilds, channels, or URLs:

| Source | Channel |
| --- | --- |
| `message` | Existing `messageLogChannelID` in `discord/audit_logger.go` |
| `reaction` | Existing `reactionLogChannelID` in `discord/audit_logger.go` |
| `server` | Existing `serverLogChannelID` in `discord/audit_logger.go` |
| `moderation` | Configured `DiscordLogChannel` (`discord_log_channel`) |

The adapter verifies the channel belongs to the configured Discord guild, and
uses the bot's existing authenticated disgo REST client, context cancellation and
rate limiter. Blank moderation channel configuration or a missing Discord client
returns a useful unavailable error. Missing history permissions/API failures
return a sanitized error; stored-case browsing continues to work.

```json
{"items":[{"id":"1480000000000000000","timestamp":"2026-03-29T12:00:00Z","source":"message","content":"Message deleted\nAttachment: details.txt\n…","url":"https://discord.com/channels/111/222/1480000000000000000","attachments":[{"name":"details.txt","url":"https://cdn.discordapp.com/attachments/222/333/details.txt?ex=…&hm=…"}]}],"next_cursor":"1479999999999999999","scanned":200,"complete":false,"warnings":["Dates match the posted log message in UTC, not necessarily the original event or message date."]}
```

### Coverage and pagination

Each request starts directly at the exclusive next-day snowflake bound, so an
old date does not require scanning recent history. Existing channel history is
read on demand, not only events received since this feature was enabled. Deleted
Discord posts, excluded/unlogged events, and history inaccessible to the bot are
not recoverable. These are actual posted log messages, not reconstructed events:
**their timestamp is the posted log date, which may differ from the original
message or event date**.

Each request returns at most 50 matches and processes at most 200 messages. The
cursor follows the earliest **processed** message, not the end of an otherwise
unprocessed REST batch, so matches cannot be skipped at the result limit. An
empty match page can still have a continuation. Keep the same source/dates/query
and pass `next_cursor` as `before` until it is empty. `complete: false` with a
cursor means there is more range to scan, even if `items` is empty. At range
exhaustion `complete` is true only if this page's text attachments were fully
searched. `complete: false` with an empty cursor and partial-search warnings means
the range is exhausted but attachment coverage is incomplete. Accumulate warnings
across all pages before claiming a complete investigation.

### Attachment and resource limits

Only `.txt` or declared `text/*` attachments are downloaded, never arbitrary
files/URLs from the caller. Downloads require HTTPS, exact `cdn.discordapp.com`
or `media.discordapp.net` hosts, an `/attachments/<id>/<id>/<filename>` path,
no credentials/ports/fragments, and public DNS addresses. Redirects are refused;
no bot Authorization header is sent. Signed URLs from Discord metadata are
expected and are returned only to authorized admins. They may expire.

Searches have a 20-second deadline and a four-request concurrency limit (hence
at most four simultaneous attachment downloads). Per search, downloads are
serial, limited to 20 requests and 1 MiB total received content; each attachment
has a three-second timeout and 128 KiB text limit. Metadata indicating an
oversized attachment skips that download. Oversized responses are truncated;
non-UTF-8/binary text, unavailable/expired files, redirects, interrupted downloads
and exhausted budgets produce explicit **partial-search warnings**, never an
unconditional complete/no-results claim. To retry missing attachment content,
repeat the same page with its input cursor; continuation intentionally advances
past processed messages. Non-text files are linked when their metadata URL is
safe, but are not content-indexed. No HTML is rendered by these APIs; consumers
must display content as plain text and treat attachment links as untrusted data.

Malformed/repeated/unknown parameters, invalid dates/ranges and noncanonical
cursors return 400. Query text is limited to 4096 bytes. Case cursors are positive
int64 case numbers; log cursors are nonzero uint64 snowflakes within the
requested date bounds. Dates never depend on the server's local timezone.

## Integration wiring

`dashboard.Handler.AuditLogs` accepts the small `auditsearch.Searcher` interface;
leaving it nil disables only log search. `main.go` sets it before serving:

```go
handler.AuditLogs = auditsearch.New(discordBot.AuditHistory())
```

`dashboard.New` keeps its existing signature. The only shared dashboard changes
are the optional field/import and authenticated GET routes for `/api/cases` and
`/api/logs`; other handlers and static assets are unchanged.
