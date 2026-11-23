# metrobot

Metrobot is Metrolist's Discord and Telegram bot for moderation, logging, dehoisting, and community support. Server, role, and channel IDs in `discord/` are hardcoded for the official server; review them before running the bot elsewhere.

## Features

- **Moderation and logging.** Slash commands and `!action` or `ok garmin` triggers handle bans, kicks, timeouts, warnings, `/dehoist`, `/approvenick`, `/bulkrole`, `/purge`, and `/scanreactions`, with Discord audit and case logging.
- **Markdown notes.** Staff manage notes with `/addnote`, `/editnote`, `/renamenote`, and `/delnote`. Users trigger notes via `/note`, `.name`, or inline `!!name`. See [note markup](docs/notes.md) for random groups, FAQs, and Components V2 layouts.
- **AI chat and support.** Discord AI chat and `#app-support` routing use OpenRouter models, staff notes, embedded guides, and public GitHub lookups. Telegram has no AI chat handler. See [support routing](docs/support-routing.md).
- **Releases and nightlies.** `/version` and `/latest` show GitHub releases. `/actions` reads uploaded Android, Windows x64, and Linux x64 assets from the `nightly` prerelease with a backup and instability warning. Startup accepts legacy `actions_workflow_file` and `actions_artifact_names` config keys and logs that it ignores them.
- **Hardware specs.** `/gsm`, `/nanoreview`, `/technicalcity`, and the `*compare` commands search and compare phones, CPUs, GPUs, laptops, and SoCs.
- **Media and Discord utilities.** `/quote`, the `Clip` message command, and `ogc` render quote images. `/nuke` renders avatar GIFs locally. `OCR Translate` translates image or message text in place via OpenRouter vision without chat history or shared memory. YouTube links have `?si` and `?is` tracking parameters stripped, and starboard plus sobboard track starred messages.
- **Fun, kudos, and reminders.** Discord supports `>>wa`, `>>daily`, `>>lb`, `>>starlb`, `>>slots`, `>>bj`, and `>>rmme` with DM fallback. Wrap `>>` commands inside `{{...}}` blocks to run them inline within a message in order. Run `/help` for the full command list.

## Configuration and running

Build with the Go version in `go.mod`, currently Go 1.27.3. Copy `config.example.json` to `config.json` and fill in tokens, guild and chat IDs, admin IDs, and repository fields. Leave unused optional key arrays like `openrouter_api_keys`, `firecrawl_api_keys`, or `wolfram_app_ids` as `[]` or omit them; `config/config.go` rejects blank strings inside any key list.

```sh
go build -o metrobot .
./metrobot
```

The bot expects `config.json` and `bot.db` in its working directory and opens no inbound HTTP listener. When `garmin_memory_file` is empty, `discord/bot.go` checks `garmin-memory.md` first and falls back to `/data/garmin-memory.md`. Keep `config.json`, `.env`, `bot.db*`, and `bot.log` out of Git.

## Deployment and upgrades

Production runs as a compiled Go binary at `/opt/metrobot/metrobot` managed by the `metrobot` `systemd` service, keeping `config.json`, `bot.db`, `bot.log`, and the live `garmin-memory.md` in its working directory. `Dockerfile` and `.github/workflows/docker.yml` also build a `gcr.io/distroless/static-debian12:nonroot` Linux amd64 container on pushes to `main`, publish it to GHCR, and seed `/data/garmin-memory.md`.

Before upgrading, back up the runtime directory and preserve `bot.db`, `config.json`, and your live memory file. Never overwrite production data with `garmin-memory.md` or test databases from Git. Startup migrations `note_markup_v1` and `metrolist_v14_notes_v1` run once per database; see [Metrolist v14 note migration](docs/notes.md#metrolist-v14-note-migration).

## Documentation

- [Note markup and migrations](docs/notes.md)
- [AI chat and support routing](docs/support-routing.md)
- [Local note and AI text editor](tools/note-editor/README.md)
