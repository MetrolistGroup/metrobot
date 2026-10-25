# metrobot

this is the discord and telegram bot we use for logging, punishments, dehoisting, and group management


## Selfhosting?

do you want to selfhost this bot? don't. it's pretty custom-made for us, it won't fit your needs in most cases, and some stuff is hardcoded

## Bot data

The bot has no web interface or inbound HTTP listener.

- [Note markup](docs/notes.md): random groups, interactive FAQs and authored Components V2 layouts. The bot migrates existing donate/KMP behavior once. Behavior depends on note content, not names.
- [Automatic app-support routing](docs/support-routing.md): opt-in OpenRouter Decisions routing, note eligibility, privacy and rollout checks.
- [Garmin base prompt](docs/prompt.md): stored overrides survive restarts and apply to new AI requests.

Admins manage shared AI memory with `/memory view|append|replace|clear` when AI is enabled. Shared memory is limited to 16 KiB including its trailing newline.

Keep the existing `bot.db` and shared memory file when replacing the binary. Back up the database before upgrading. The first markup-enabled run migrates legacy note behavior into explicit markup. Already-migrated databases must retain that markup and migration metadata.
