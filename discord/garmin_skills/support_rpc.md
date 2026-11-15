# Discord Rich Presence (RPC) setup reference

Scope: Metrolist v14 on Android 7+, iOS 17+, Windows 10+, macOS 15+ and Linux unless stated otherwise. The KMP rewrite is now Metrolist in the public MetrolistGroup/Metrolist repository. Menu details below come from pre-v14 source review, not v14 device tests, and may differ. Ask for platform/version and the visible label if needed.

Start with the account/profile menu > Integrations > Discord, Log in, complete authorization in the app's flow, enable Rich Presence and play music. Integrations was beside Settings, not inside the main Settings list. Google/YouTube Music login is not required for Discord setup. Account is a heading, not an extra button.

Pre-v14 rewrite labels included Enable Discord Rich Presence, Log out and Advanced mode. Android tried the Discord app before a browser fallback; desktop used an embedded authorization window; iOS used the system URL opener. Do not promise a universal external-browser handoff. Missing WebViews or unwritable storage can block sign-in; no universal remedy is verified.

Turning the presence switch off stops sharing but retains login. Log out removes app authorization after confirmation; local logout does not prove server-side revocation. The rewrite attempted to clear presence when paused; iOS background reliability is unverified. Refresh RPC resubmitted presence but is not a confirmed fix.

Historical standalone Android 13.x used Discord Integration > Log in > Enable Rich Presence under Options. It could retain paused metadata and disconnect later. These labels and timers may differ in v14. Do not apply old defaults to v14.

For missing presence, check connection and enable switch. Invisible status hides presence; try Online, Do Not Disturb or Idle. Discord may hide your own profile buttons, so ask someone else to view them. Preview is not proof of public visibility. Never prescribe desktop IPC setup or token extraction.

Never request passwords, cookies, tokens, callback URLs, HARs, backups or credential-bearing logs/screenshots in chat or tools. Credentials belong only in the app. Never prescribe reinstalling, clearing app data or logout as routine troubleshooting. Back up before a source-supported destructive action.
