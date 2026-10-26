# Metrolist YouTube Music playlists

Use for account playlists and synchronization. Music-account login is supported, including in standalone Android; it is not a KMP-only feature. Match variant/version/platform before giving different steps.

## Verified scopes

- **A:** standalone Android v13.7.0, `MetrolistGroup/Metrolist@7c5ba3ad22cb562d337897f82163a2b7fce665f3`.
- **M:** Android source snapshot `876e355181fd3b04e7063b10442fef1b721d53b3`. The entry flow matches A. Deletion handling differs as noted below.
- **K:** KMP source snapshot `MetrolistGroup/Metrolist-KMP@c1e669d51ee021ca96f60ea9914b0d6b5aba8ad6` covers Android, desktop and iOS. It does not prove availability in a distributed KMP release.

## Get existing YouTube Music playlists into Metrolist

1. Sign into the Google/YouTube Music account and YouTube channel holding the playlists. **A/M:** Home → top-bar profile (**Account**) → **Login**. **K:** top-bar profile → **Sign in**. Use the login guide for platform-specific authorization; do not invent a common browser or channel-picker flow. Signed-in menus offer **Switch YouTube channel** if needed.
2. Open **Library** → **Playlists**. Account playlists, including saved playlists, do not require individually pasting/importing their links. **A/M:** leave the profile popup's **Auto-sync with account** enabled (default on). Profile → signed-in account name → **Playlists** also browses the remote account library directly.
3. Only if a refresh is needed: **A/M**, check **Auto-sync with account**, then reopen Library → Playlists; entry queues synchronization. **K Android/iOS**, pull down in Library; **K desktop**, use top-bar **Refresh** while Library is open. Network/account access is required. An empty response does not prove that playlists or login are unsupported.

For a simple setup request, give sign-in and Library steps, then stop. Do not add import/editing internals or promise instant, complete synchronization.

## What sync means

- **A/M:** account sync pulls the saved-playlist list and tracks from YouTube Music. Home startup/refresh also requests sync, subject to a 30-minute automatic cooldown; Library → Playlists has a separate queued sync. It is not continuous background mirroring. A can preserve locally stored songs removed remotely and skip empty remote playlists. M has different reconciliation that preserves pending additions but can remove confirmed remote occurrences. Do not promise identical deletion propagation or lossless two-way sync.
- **K:** Library refresh reconciles account playlist membership/metadata. Known remote-library removals require two complete refreshes. Opening a remote playlist loads tracks and may replace its local cache. Do not promise all stored tracks continuously refresh. No Android-style **Auto-sync with account** playlist toggle was verified in K.
- A saved remote-linked playlist is different from an independent local copy. Manual import/copy does not continually sync with the source playlist. Local playlists are not automatically uploaded. Creating/selecting a YouTube Music destination can write to the account.
- Editing depends on the playlist's editability, not just its presence in Library. Saving someone else's playlist does not grant editing rights. On editable remote-linked playlists, add/remove/rename/reorder/delete actions can affect YouTube Music too. **Auto-sync with account** is not a read-only safety switch: separate edit handlers can still make remote requests.
- Do not invent an **Import playlist** menu on A/M: the traced online-playlist menu exposes **Add to playlist**, while the import dialog is unreachable there. Manual import UI differs across KMP platforms; obtain the platform and verified steps before describing it. Library metadata is not downloaded audio. Large, private, region-restricted or unavailable-track imports have no universal completeness guarantee.

## Safety

Never suggest deletion, logout **Clear**, clearing app data or reinstalling as a refresh fix. Preserve/back up local-only playlists before destructive actions. Do not request passwords, cookies, tokens, HARs, private playlist links or credential-bearing screenshots/logs in chat or bot tools. Sign-in and any link entry belong in the user's own app. Never ask users to make private playlists public or assume a new remote playlist's visibility.

## Immutable provenance / limits

Paths are relative to the repositories and revisions above. Source/resources/callers were read only; no hardware, packaged-app, account-login or live sync/import testing. If UI differs, ask for variant/version/platform and a non-secret symptom rather than inventing a menu.

**A/M**, prefix `app/src/main/kotlin/com/metrolist/music/`:
- Account/channel/toggle: `MainActivity.kt`, `ui/component/Dialog.kt`, `ui/screens/settings/AccountSettings.kt`, `ui/screens/LoginScreen.kt`.
- Library and refresh: `ui/screens/AccountScreen.kt`, `viewmodels/AccountViewModel.kt`, `ui/screens/library/{LibraryScreen,LibraryPlaylistsScreen}.kt`, `viewmodels/{PlaylistsViewModel,HomeViewModel}.kt`, `utils/SyncUtils.kt`, `constants/PreferenceKeys.kt`.
- Copy/edit/delete: `ui/screens/playlist/{OnlinePlaylistScreen,LocalPlaylistScreen}.kt`, `ui/menu/{YouTubePlaylistMenu,ImportPlaylistDialog,AddToPlaylistDialog,PlaylistScreenMenus,PlaylistMenu}.kt`, `ui/component/CreatePlaylistDialog.kt`, `db/entities/PlaylistEntity.kt`.
- Labels: `app/src/main/res/values/{strings,metrolist_strings}.xml`.

**K:** `shared/src/commonMain/kotlin/com/metrolist/shared/` contains `ui/components/{AccountMenu,AccountPopupDialog}.kt`, `ui/mobile/{MobileLibrary,MobileDetailScreens}.kt`, `ui/screens/{LibraryContent,ScreenContentText}.kt`, `viewmodels/LibraryViewModel.kt`, `api/YouTubeRepository.kt`, `db/PlaylistSync.kt`, `playlist/PlaylistMutations.kt`, `playlistimport/{PlaylistImportManager,UnifiedPlaylistImporter}.kt`, `api/YouTubePlaylistEdits.kt`.
- Android callers: `androidApp/src/main/kotlin/com/metrolist/android/ui/{screens/LibraryScreen,screens/PlaylistScreen,MetrolistApp}.kt`.
- Desktop callers: `desktopApp/src/jvmMain/kotlin/com/metrolist/desktop/ui/{screens/LibraryScreen,layouts/AppLayouts,components/TopBar}.kt`, `desktopApp/src/jvmMain/kotlin/com/metrolist/desktop/state/content/{LibraryContentState,PlaylistAlbumState}.kt`.
- K labels: `shared/src/commonMain/composeResources/values/strings.xml`. Login handoffs have additional pinned provenance in `support_login.md`.
