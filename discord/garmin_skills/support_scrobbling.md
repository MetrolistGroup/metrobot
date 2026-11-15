# Scrobbling setup reference

Scope: Metrolist v14 on Android 7+, iOS 17+, Windows 10+, macOS 15+ and Linux unless stated otherwise. The KMP rewrite is now Metrolist in the public MetrolistGroup/Metrolist repository. Menu details below come from pre-v14 source review, not v14 device tests, and may differ. Ask for platform/version and the visible label if needed.

Metrolist v14 supports Last.fm and ListenBrainz. Now Playing is live status, not proof that a scrobble reached history. Start at account/profile > Integrations and choose the service; pre-v14 labels may differ.

Pre-v14 rewrite:
1. Last.fm > Connect your Last.fm account > Username and Password in the app's Connect Last.fm dialog > Connect. Confirm Logged in as, then Enable scrobbling. Now Playing updates is separate. No browser or token-copy step is needed for Last.fm.
2. ListenBrainz > Connect with a ListenBrainz user token > Connect ListenBrainz. The User token link opens https://listenbrainz.org/settings/. Copy your own token only into the app's User token field, then Connect. Check Enable scrobbling. Do not invent website token-generation controls.
3. Each service has its own switch. Show on Home changes display, not submission.

Rewrite timing defaults were known duration of at least 30 seconds, then the lesser of 50% duration or 180 seconds of accumulated playing time. Unknown duration was ineligible; pauses did not count. Retryable failures persisted up to 200 pending entries total with oldest discarded; account/permanent failures did not retry. Disconnect/reconnect cleared that provider's queue, so never suggest it as a routine fix. These internals are historical, not a v14 reliability guarantee.

Historical standalone Android 13.x used Integrations > Last.fm integration > Log in with in-app Username/Password > Login, then Options > Enable scrobbling, default off. Its default minimum total duration was strictly greater than 30 seconds, with listening threshold min(50%, 180 seconds). A 40-second song needed 20 seconds of playback, not 30. It had no persisted offline queue. Do not apply this Last.fm-only scope or those defaults to v14.

Check service connection and switch, then play a fresh known-duration track online and check history separately from Now Playing. Both services send title, artist, optional album/duration and playback-start time. Local/downloaded playback still needs network for submission. Ask for service, platform/version and non-secret error if unresolved.

Never request passwords, cookies, tokens, callback URLs, HARs, backups or credential-bearing logs/screenshots in chat or tools. Credentials belong only in the app. Never prescribe reinstalling, clearing app data or logout as routine troubleshooting. Back up before a source-supported destructive action.
