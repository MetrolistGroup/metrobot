# Metrolist YouTube Music playlists

Scope: Metrolist v14 on Android 7+, iOS 17+, Windows 10+, macOS 15+ and Linux unless stated otherwise. The KMP rewrite is now Metrolist in the public MetrolistGroup/Metrolist repository. Menu details below come from pre-v14 source review, not v14 device tests, and may differ. Ask for platform/version and the visible label if needed.

Google/YouTube Music login and account playlists are supported. Use the login guide for the platform flow, select the intended YouTube channel, then open Library > Playlists. Do not require pasting every playlist link or promise instant complete synchronization.

Pre-v14 rewrite: Android/iOS refreshed by pulling down in Library; desktop used top-bar Refresh while Library was open. Account playlist membership/metadata reconciled on refresh; known remote removals needed two complete refreshes. Opening a remote playlist could replace its local track cache. These details may differ in v14; do not invent an Auto-sync toggle from 13.x.

Historical standalone Android 13.x: Home > profile Account > Login, then Library > Playlists. The profile's Auto-sync with account switch enabled automatic pulls. Turning it off stopped automatic pulls without removing existing items or blocking manual sync and account writes. Reopening Library > Playlists queued sync. No verified pull-to-refresh or toggle cycle applied there. These paths may differ in v14.

Remote-linked playlists differ from independent local copies. Manual copies do not continuously sync. Saving someone else's playlist does not grant editing rights. Edits to an editable remote-linked playlist can affect YouTube Music; a sync switch is not a read-only safety control. Do not promise lossless two-way sync, deletion propagation or every track matching.

Metrolist v14 imports backups/playlists from 30+ clients and 6 services, including Spotify playlists; it plays YouTube Music matches, not Spotify audio. Platform import menus may differ. Library metadata is not downloaded audio. Never ask users to make private playlists public or send private links to bot tools.

Never request passwords, cookies, tokens, callback URLs, HARs, backups or credential-bearing logs/screenshots in chat or tools. Credentials belong only in the app. Never prescribe reinstalling, clearing app data or logout as routine troubleshooting. Back up before a source-supported destructive action.
