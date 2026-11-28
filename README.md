# metrobot

Metrobot is Metrolist's Discord and Telegram bot for moderation, logging, dehoisting, and community support. Server-specific channels, welcome roles, moderation rules, and AI routing live in `config.json`.

## Features

- **Moderation and logging.** Slash commands and `!action` or `ok garmin` triggers handle bans, kicks, timeouts, warnings, `/dehoist`, `/approvenick`, `/bulkrole`, `/purge`, and `/scanreactions`, with Discord audit and case logging.
- **Markdown notes.** Staff manage notes with `/addnote`, `/editnote`, `/renamenote`, and `/delnote`. Users trigger notes via `/note`, `.name`, or inline `!!name`. See [note markup](docs/notes.md) for random groups, FAQs, and Components V2 layouts.
- **AI chat and support.** Discord AI chat and `#app-support` routing use OpenRouter models, staff notes, embedded guides, and public GitHub lookups. Telegram has no AI chat handler. See [support routing](docs/support-routing.md).
- **Releases and nightlies.** `/version` and `/latest` show GitHub releases. `/actions` reads uploaded Android, Windows x64, and Linux x64 assets from the `nightly` prerelease with a backup and instability warning. Startup accepts legacy `actions_workflow_file` and `actions_artifact_names` config keys and logs that it ignores them.
- **Hardware specs.** `/gsm`, `/nanoreview`, `/technicalcity`, and the `*compare` commands search and compare phones, CPUs, GPUs, laptops, and SoCs.
- **Media and Discord utilities.** `/quote`, the `Clip` message command, and `ogc` render quote images. `/nuke` renders avatar GIFs locally. `OCR Translate` translates image or message text in place via OpenRouter vision without chat history or shared memory. YouTube links have `?si` and `?is` tracking parameters stripped, and starboard plus sobboard track starred messages.
- **Fun, kudos, and reminders.** Discord supports `>>wa`, `>>daily`, `>>lb`, `>>starlb`, `>>slots`, `>>bj`, `>>kud`, `>>mines`, `>>highlow`, `>>fight`, `>>plinko`, `>>crash` or `>>coins-crash`, `>>loan`, `>>shop`, `>>buy`, `>>gc @user`, and `>>rmme` with DM fallback. Wrap `>>` commands inside `{{...}}` blocks to run them inline within a message in order. Run `/help` for the full command list.

## Configuration and running

Build with the Go version in `go.mod`, currently Go 1.27.3. Copy `config.example.json` to `config.json` and fill in tokens, the Discord guild ID, Telegram chat ID, and repository fields. The example keeps the official server's feature IDs. Replace those IDs for another deployment, or clear optional IDs to disable those features. Leave unused optional key arrays like `openrouter_api_keys`, `firecrawl_api_keys`, or `wolfram_app_ids` as `[]` or omit them; `config/config.go` rejects blank strings inside any key list.

The configuration has one `discord` section and one `telegram` section. Discord settings are grouped under `logging`, `moderation`, `welcome`, `ai`, and `starboard`. Clearing an optional log destination, support channel, welcome configuration, or starboard destination disables that feature. A welcome configuration requires its role, channel, and note together. AI community-channel aliases and descriptions are configured in `discord.ai.readable_channels` and `discord.ai.channel_descriptions`. Empty maps and lists add no server-specific defaults.

Access comes from native platform permissions. Stored bot-admin records and legacy `permaadmin_*` arrays grant no access; `/addadmin` and `/removeadmin` are removed. Normal members can still read notes and use public utilities.

| Feature | Discord permission | Telegram permission |
| --- | --- | --- |
| Ban, temporary ban, softban, ban and delete | Ban Members | Ban users |
| Kick | Kick Members | Not exposed on Telegram |
| Timeout, warn, remove warning | Moderate Members | Ban users |
| Edit notes | Manage Server | Change group info |
| AI memory and bot settings | Manage Server | Not exposed on Telegram |
| Dehoist, approve nicknames | Manage Nicknames | Ban users for the preview |
| Purge, scan reactions, reset channel context | Manage Messages | Not exposed on Telegram |
| Bulk role assignment | Manage Roles | Not exposed on Telegram |

Discord server owners and Administrators have these permissions. Telegram group creators have all group rights; administrators need the relevant right for the requested action. Member-management commands also check target protection and role hierarchy. Channel commands also respect channel permission overrides, including the parent channel of a thread. Failed permission lookups deny the action.

Softban bans and immediately unbans the member on either platform, allowing them to rejoin. Discord deletes recent messages; Telegram deletes the member's messages in supergroups.

When migrating an existing config, flat connection keys and existing starboard keys still load. Move them into the corresponding nested section when convenient; an explicitly supplied nested value takes precedence, including an empty destination. Add the feature sections from the example to retain the former official-server routing and logging. Features without configured IDs stay disabled. The database is retained, including old admin rows, but those rows no longer authorize actions. Restart the bot after editing configuration.

```sh
go build -o metrobot .
./metrobot
```

The bot expects `config.json` and `bot.db` in its working directory and opens no inbound HTTP listener. When `garmin_memory_file` is empty, `discord/bot.go` checks `garmin-memory.md` first and falls back to `/data/garmin-memory.md`. Keep `config.json`, `.env`, `bot.db*`, and `bot.log` out of Git.

Discord shows the custom status "Keeping Metrolist on track 🎵" while the bot is connected.

## Deployment and upgrades

Production runs as a compiled Go binary at `/opt/metrobot/metrobot` managed by the `metrobot` `systemd` service, keeping `config.json`, `bot.db`, `bot.log`, and the live `garmin-memory.md` in its working directory. `Dockerfile` and `.github/workflows/docker.yml` also build a `gcr.io/distroless/static-debian12:nonroot` Linux amd64 container on pushes to `main`, publish it to GHCR, and seed `/data/garmin-memory.md`.

File logs rotate at 10 MB and retain at most two compressed archives for seven days.

Before upgrading, back up the runtime directory and preserve `bot.db`, `config.json`, and your live memory file. Never overwrite production data with `garmin-memory.md` or test databases from Git. Startup migrations `note_markup_v1` and `metrolist_v14_notes_v1` run once per database; see [Metrolist v14 note migration](docs/notes.md#metrolist-v14-note-migration).

## Documentation

- [Note markup and migrations](docs/notes.md)
- [AI chat and support routing](docs/support-routing.md)
- [Local note and AI text editor](tools/note-editor/README.md)
