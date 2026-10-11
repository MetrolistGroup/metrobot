# metrobot

this is the discord and telegram bot we use for logging, punishments, dehoisting, and group management


## Selfhosting?

do you want to selfhost this bot? don't. it's pretty custom-made for us, it won't fit your needs in most cases, and some stuff is hardcoded

## Local admin dashboard

The dashboard is optional and disabled by default. Its dark-only BeerCSS interface provides syntax-highlighted notes and shared-memory editors, all stored moderation cases, historical Discord log search, and a live Garmin base-prompt editor. Only existing Discord bot admins and configured permanent admins can sign in; Discord guild ownership or administrator permissions alone do not grant access. Only permanent admins can manage bot admins or edit the system prompt. No tokens or credential configuration are exposed.

- [Note markup](docs/notes.md): explicit randomized groups, interactive FAQs and authored Components V2 layouts; existing donate/KMP behavior is migrated once and follows the content, not the name.
- [Investigations](docs/investigations.md): paginated cases and inclusive UTC date/text searches through existing message, reaction, server-event and moderation log channels, including bounded text-attachment searches.
- [Garmin base prompt](docs/prompt.md): persistent live replacement/reset without changing code-enforced permissions or separate routing/summarization prompts.
- [Dashboard assets](docs/dashboard-assets.md): pinned self-hosted BeerCSS/Prism, syntax highlighting and verification guidance.

For a local setup:

1. In the Discord Developer Portal, select an application (preferably the same application as the bot). Under **OAuth2**, add the exact redirect URL `http://127.0.0.1:8080/auth/callback`.
2. Set the `dashboard` block in your own local `config.json` using the fields in `config.example.json`: `enabled: true`, `listen_address: "127.0.0.1:8080"`, `base_url: "http://127.0.0.1:8080"`, and the application's OAuth2 client ID and client secret. The OAuth client secret is distinct from the bot token. Keep credentials private and out of Git; never paste them into requests for support.
3. Run the bot locally and open `http://127.0.0.1:8080`. Discord login requests only the `identify` scope. Your Discord user ID must already be in `permaadmin_discord_ids` or the bot's Discord admin database.

`base_url` must be an origin with no trailing slash, path, query, fragment or userinfo. The registered callback is always `base_url + "/auth/callback"`. For HTTP loopback origins, an omitted `listen_address` uses the base URL's host and port (port 80 if omitted), so IPv6 origins bind IPv6 loopback. For HTTPS origins, it defaults to `127.0.0.1:8080` for separate TLS termination. Plain HTTP base URLs are allowed only on loopback. The listener always uses plaintext HTTP and must bind loopback; HTTPS origins require TLS termination on the same machine with the configured Host preserved. Do not expose this local setup to the internet. The dashboard stays disabled unless explicitly enabled. Back up the database before upgrading: the first run migrates legacy note behavior into explicit markup.

Sessions are opaque, server-side, expire after eight hours, and are lost on restart. Login state expires after five minutes and is one-use/browser-bound; pending logins and active sessions are each capped at 256. Writes and logout require CSRF tokens, and bot admin access is rechecked on every private request. Auth/private responses cannot be cached. Request bodies are limited to 128 KiB; shared memory remains limited to 16 KiB including its trailing newline. Notes use the same validation as bot commands, with no separate content-length cap beyond the request-body bound. Ordinary notes that exceed Discord's 2000 UTF-16-unit message limit or Telegram's 4096-unit limit (checked conservatively against serialized HTML too) are delivered as a full `note.md` Markdown attachment, without splitting or blocking saves. Short notes retain their existing rendering, reply, visibility and cleanup behavior. Authored `faq` blocks provide the paginated FAQ presentation for `## ` question headings (up to 25 questions). New layouts are validated against Discord limits; oversized legacy FAQs still fall back to readable text or an attachment. The memory editor is disabled when AI is disabled. Concurrent edits are last-write-wins, including edits made through bot commands; reload before editing if another admin may have changed the data. Notes are read raw for editing, so the `donate` note is not shuffled in the dashboard. Dashboard creation and edits preserve raw literal escape sequences; description-only edits also preserve existing line endings. Editing the text itself uses the browser's LF line endings. Command normalization preserves JSON escapes inside custom blocks; attachments preserve the full readable delivery text before platform rendering.
