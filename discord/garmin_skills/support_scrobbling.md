# Verified scrobbling setup

Scrobbling sends Metrolist playback history; Now Playing is separate live status, not proof of a submitted scrobble.

## Build scope

- **A: standalone Android v13.7.0 stable**, `Metrolist@7c5ba3ad22cb562d337897f82163a2b7fce665f3`: Last.fm only; no Libre.fm or ListenBrainz integration found.
- Android source snapshot `876e355181fd3b04e7063b10442fef1b721d53b3` is not stable. Its Last.fm UI, manager and API match A.
- **K: KMP source**, `Metrolist-KMP@c1e669d51ee021ca96f60ea9914b0d6b5aba8ad6`. Android, desktop and iOS support Last.fm and ListenBrainz. No Libre.fm integration was found. This local snapshot does not verify released-build availability.

Default English labels; match app/build, version and platform first.

## Standalone Android v13.7.0 (A)

1. Account/avatar button → **Integrations** → **Last.fm integration**. Integrations is beside **Settings** in the account dialog, not inside Settings.
2. Under the **Account** heading, tap **Log in**, enter Last.fm **Username** and **Password** in the app, then tap **Login**. **Account** is a section heading, not an extra button. This uses mobile-session authentication, not browser authorization or token entry.
3. After the account name appears, turn on **Options** → **Enable scrobbling** (default off). Optional **Send Now Playing status** (default off) requires login and scrobbling enabled. **Sync Loved and Unloved** syncs likes/unlikes; not required for scrobbles.

**Scrobbling configuration** defaults: **Scrobble songs longer than (seconds)**: the track's TOTAL DURATION must be strictly >30s; **Scrobble delay (percentage)**: 50%; **Scrobble delay (minutes)**: 3 minutes (180s). Submission waits the lesser delay in playing time. The 30-second setting is not listening time: do not say "play for 30 seconds to scrobble". For example, an eligible 40-second track has a 20-second listening threshold; a four-minute track has a two-minute threshold. Pause/resume preserves the timer; stopping/changing tracks cancels it.

**Offline:** Direct submission; no persisted offline queue or retry/backfill. Downloads do not ensure offline listens reach Last.fm later. Accepted scrobbles are not checked in the response, so login/Now Playing does not prove acceptance.

## KMP at K (source-scoped)

Account/profile menu → **Integrations**, then choose the service; **Settings** is a separate entry.

1. **Last.fm** → **Connect your Last.fm account** → enter **Username** and **Password** in **Connect Last.fm** → **Connect**. This obtains a mobile session directly, with no browser/token-copy step. Confirm **Logged in as …**, then **Enable scrobbling**; optional **Now Playing updates**. Both preferences default on.
2. **ListenBrainz** → **Connect with a ListenBrainz user token** opens the **Connect ListenBrainz** dialog, not a website. The **User token** link inside that dialog opens `https://listenbrainz.org/settings/`. Copy your own token there (token-generation controls on that website are not verified); paste it **only into the app's User token field**, then **Connect**. Validation precedes **Connected as …** and automatically enables this service. Check **Enable scrobbling**; optional **Now Playing updates** defaults on.
3. Each service has **Enable scrobbling**; no all-services master switch is exposed. Last.fm also gates on `lastFmActivityEnabled` (default true; no UI setter found). **Show on Home** affects display, not submission.

**Timing:** Last.fm's **Sensitivity** controls **Scrobble delay**, **Minimum song duration**, **Max delay** for **both services**. Defaults: duration at least 30 seconds, then the lesser of 50% of duration or 180 seconds of accumulated playing time. Unknown duration is ineligible; pauses do not count. Pause/stop check the threshold.

**Offline:** Retryable/network failures persist: **200 pending entries total**, oldest discarded. Retries use backoff while the manager runs, network is connected, reporting ownership is enabled and the same account is connected. Now Playing is not queued; authentication/permanent failures are not retried, but accounts stay connected. Service switches gate new reports, **not queued retries**. Disconnect/reconnect clears that provider's queue: never suggest it as a generic fix.

## What is sent and how to check

Both send title, artist, optional album and duration, with playback-start time for history. A joins artists; K uses the first. Neither manager excludes local/downloaded tracks; submission needs network. Now Playing at track start is separate from history: Last.fm `track.updateNowPlaying` / `track.scrobble`; ListenBrainz `playing_now` / `single`.

Check connection and the service switch, then play a fresh known-duration track past the threshold online. Check history separately from Now Playing. If unresolved, ask for build/platform/version, service, missing status versus history, and non-secret error; never diagnose from the error alone. Never send passwords, tokens, session keys, cookies or backups to Metrobot. Credentials go only in the app's designated fields. No routine data clearing, reinstall or disconnect.

## Immutable source provenance / limits

Paths use A/K above; prefixes apply to listed filenames.

- **A**, `app/src/main/kotlin/com/metrolist/music/`: `ui/screens/settings/AccountSettings.kt`, `ui/screens/settings/integrations/IntegrationScreen.kt`, `ui/screens/settings/integrations/LastFMSettings.kt`, `constants/PreferenceKeys.kt`, `playback/MusicService.kt`, `utils/ScrobbleManager.kt`; API `app/src/main/kotlin/com/metrolist/lastfm/LastFM.kt`. Labels: `app/src/main/res/values/` → `metrolist_strings.xml`, `strings.xml`.
- **K**, `shared/src/commonMain/kotlin/com/metrolist/shared/`: `ui/components/AccountMenu.kt`, `ui/settings/SharedIntegrationsScreen.kt`, `settings/Settings.kt`, `scrobble/ScrobbleManager.kt`, `scrobble/ScrobbleSessions.kt`, `scrobble/PendingScrobbleStore.kt`, `api/lastfm/LastFM.kt`, `api/listenbrainz/ListenBrainz.kt`. Labels: `shared/src/commonMain/composeResources/values/strings.xml`.
- **K playback:** `androidApp/src/main/kotlin/com/metrolist/android/playback/MusicService.kt`; `desktopApp/src/jvmMain/kotlin/com/metrolist/desktop/state/playback/PlayerState.kt`; `shared/src/iosMain/kotlin/com/metrolist/shared/playback/IosPlaybackController.kt`.

Read-only pinned `git show` trace of resources/preferences/callers only. No build, device, live authentication/submission or released-binary verification; source is not a release promise.
