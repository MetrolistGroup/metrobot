# Playback support reference

Scope: Metrolist v14 on Android 7+, iOS 17+, Windows 10+, macOS 15+ and Linux unless stated otherwise. The KMP rewrite is now Metrolist in the public MetrolistGroup/Metrolist repository. Menu details below come from pre-v14 source review, not v14 device tests, and may differ. Ask for platform/version and the visible label if needed.

Read the playback note for first checks and the logs note for platform-specific diagnostic collection.

For Source errors, buffering or 0:00, give one or two low-risk checks: another song, YouTube Music on the same connection, close/reopen, or another available audio-quality setting. Settings > Player > Audio quality is a 13.x Android path and may differ in v14. Checks are not diagnoses or guaranteed cures. If downloaded songs work, do not ask again; this narrows the symptom without proving a network, account or extractor cause.

After failed checks or a question about a fix, search official issues and read relevant maintainer comments. Compare published nightly with stable via get_metrolist_changes(ref=nightly), then inspect a matching SHA using get_metrolist_commit. Batch independent lookups; do not repeat successful searches. Issue closure, dependency bumps or unrelated exceptions do not prove a fix.

Only a matching inspected patch in a published nightly supports a specific fix claim. A trial needs backup, in-place installation and an instability warning. Nightly publishes Android, Linux x64 and Windows x64 assets. Use actual returned links and do not promise success or infer inclusion from main alone. If no fix is established, ask for one reproducible example or the missing error/version.

Use the current logs note for platform-specific playback and crash-log collection instead of historical menu labels. If the app will not open, ask for platform and exact crash behavior. If Android rejects an in-place update, ask for the installer error.

Never request passwords, cookies, tokens, callback URLs, HARs, backups or credential-bearing logs/screenshots in chat or tools. Credentials belong only in the app. Never prescribe reinstalling, clearing app data or logout as routine troubleshooting. Back up before a source-supported destructive action.
