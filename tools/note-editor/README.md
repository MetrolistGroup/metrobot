# Local note and AI text editor

This Bun web tool edits note snapshots and AI text files locally with Markdown and Go highlighting, FAQ and Components V2 previews, and decoded Go string-literal inspection. A Go helper builds from `tools/note-editor/format` using Metrobot's real note parser and Go's syntax parser. The editor never starts the bot, runs migrations, or contacts production.

## Setup and run

Create a private directory outside Git containing identical `baseline.json` and `drafts.json` snapshots:

```json
{"source":"deployment identifier","fetched_at":"ISO timestamp","notes":[{"name":"example","short_desc":"Routing hint","content":"# Hello","markup_enabled":1}]}
```

Install dependencies and start the server:

```sh
bun install --frozen-lockfile
NOTES_DATA_DIR=/private/snapshot/path bun start
```

`HOST` defaults to `127.0.0.1` and `PORT` defaults to `8084`. Change `HOST` only to bind a trusted LAN interface, and never expose the editor publicly. Open the URL printed at startup with the fragment from `access-token` inside `NOTES_DATA_DIR`. The fragment exchanges for an `HttpOnly` `SameSite=Strict` cookie and clears from the address bar.

Saves validate input, check the optimistic revision token against concurrent tabs, and write `drafts.json` atomically while leaving `baseline.json` untouched. Clearing `short_desc` opts a note out of automatic AI routing, and saving a legacy note activates markup parsing.

## AI input inventory

Optional `ai-baseline.json` and `ai-drafts.json` files store editable AI documents separately from notes. Each item records its byte limit and deployment destination; see [the AI input map](AI-INPUTS.md). Metrobot never loads an `AGENTS.md` file.

To initialize AI snapshots once, export a private `ai-runtime.json` from production with the running commit SHA, clean flag, binary hash, live memory path and content, nullable `garmin_base_system_prompt`, model IDs, and agent-file check results, then run:

```sh
bun import-ai.ts /private/snapshot/path
```

The importer verifies `HEAD` matches the deployed revision, reads tracked source via `git show`, and refuses to overwrite existing AI baselines or drafts. Go edits validate syntax in place, not full compilation, and AI Markdown is never parsed as note directives.

## Applying edits

There is no upload endpoint. Use `/api/export` or inspect `drafts.json` and `ai-drafts.json`, diff edited items against their baselines, back up live SQLite with SQLite's backup API, and apply only changed rows inside a transaction. Keep migration markers and unchanged `markup_enabled` flags intact.

Prompt overrides update `garmin_base_system_prompt` and memory edits update the live `garmin-memory.md` file up to 16 KiB. Changes to Go files or embedded `discord/garmin_skills/*.md` files require rebuilding, testing, and redeploying the binary.

## Tests

```sh
bun test
```

Tests verify cookie auth, origin checks, note validation, atomic saves, conflict handling, separate AI persistence, Go syntax checks, byte limits, and source coverage.
