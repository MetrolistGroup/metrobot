# Temporary notes and AI-text workshop

Bun serves a private, dark editor with Markdown/Go highlighting, Components V2 / interactive FAQ previews, and decoded Go string-literal inspection. A small Go helper reuses Metrobot's actual note parser and Go's standard syntax parser. No bot process, database migrations, or production connections are started by the editor.

Prepare a **private directory outside Git** containing identical `baseline.json` and `drafts.json` snapshots:

```json
{"source":"deployment identifier","fetched_at":"ISO timestamp","notes":[{"name":"example","short_desc":"Routing hint","content":"# Hello","markup_enabled":1}]}
```

Run from this directory, using a free port allowed by the host firewall:

```sh
bun install --frozen-lockfile
HOST=192.168.0.158 PORT=8084 NOTES_DATA_DIR=/private/snapshot/path bun start
```

Open `http://HOST:PORT/#ACCESS_KEY`, using the generated `access-token` file in that private directory. The fragment is exchanged for an HttpOnly, SameSite cookie and removed from the address bar. Assets are bundled locally, with no CDN dependencies. Use this HTTP service only on a trusted LAN; do not expose it publicly.

Save with the button or Ctrl+S / Cmd+S. Saves are validated and atomically written to `drafts.json`; the fetched baseline is untouched. Unsaved text has browser recovery and a leave-page warning. Conflicting saves from another tab are rejected. Blank descriptions opt notes out of AI routing. Editing a legacy note activates authored markup, matching the bot's edit behavior.

## AI input inventory

The optional `ai-baseline.json` and `ai-drafts.json` add AI documents without touching note drafts. Each document records its source, UTF-8 byte limit and exact deployment binding: SQLite prompt setting, runtime memory file, compiled source, or reference-only. The [complete input map](AI-INPUTS.md) distinguishes editable authored text from request-specific Discord/history/tool data. Metrobot does not load an AGENTS.md file.

To initialize once, fetch a private `ai-runtime.json` with the deployed binary's Git revision/clean flag/hash, live memory path/content, nullable `garmin_base_system_prompt` override, safe model IDs and discovered agent-file paths. Do not include credentials or conversation history. Build the current parser helper, then run:

```sh
bun import-ai.ts /private/snapshot/path
```

The importer requires an exact clean deployed-source match with HEAD, reads immutable source through `git show`, and refuses to replace existing AI baselines or drafts. It includes every model-call site, all embedded Markdown guides, tool descriptions/schemas, context/retry strings and tool-result producers. Go files are edited in place with all decoded literals available alongside them; saving checks syntax, not full compile correctness. AI Markdown is literal Markdown, never interpreted as note directives. Downloading the working copy includes both snapshots.

This deliberately edits existing destinations only. There is **no upload endpoint**. After the user confirms completion, compare only edited rows against their live baselines, back up the live database with SQLite's backup API, and update those notes transactionally. Never replace the production database with a snapshot: it contains unrelated live bot data. Preserve unchanged note interpretation and all migration metadata. AI prompt-setting and memory edits need their own baseline checks and backups. Go/embedded-guide edits require source integration, build/tests and a binary redeployment; merely uploading source files will not activate them. Resolve a simultaneous base-prompt override and built-in-prompt edit explicitly. Restarts clear in-memory conversations.

```sh
bun test
```

Tests cover backend authentication, CSRF protection, canonical note parsing, invalid-edit rejection, legacy preservation, atomic saves, concurrent-edit conflicts, separate AI persistence, Go syntax/literal extraction, memory byte limits, reference-only enforcement and source coverage. They use temporary fixtures and never contact production.
