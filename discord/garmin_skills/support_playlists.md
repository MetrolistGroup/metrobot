# Metrolist YouTube Music playlists

Match variant/version/platform first. English labels. Music-account login, not Discord login. Use only the relevant steps below; ordinary account playlists do not require a separate manual import.

## Source applicability

- **A: standalone Android v13.7.0**, `7c5ba3ad22cb562d337897f82163a2b7fce665f3`, repository `MetrolistGroup/Metrolist`.
- **M: standalone Android main snapshot**, `6d08504262de76a4ba841dc24489a754e2f48e4f`, same repository. Entry paths below match A; synchronization differences are called out.
- **K: KMP snapshot**, `2ac8e47c5566b5771133d798618d75cc69c2d039`, repository `MetrolistGroup/Metrolist-KMP`. Not proof of features/access in a user's KMP release or of current remote main.

## Minimum steps: see existing account playlists

1. Sign into the Google/YouTube Music account and intended **YouTube channel** that holds the playlists. **A/M:** Home → top-bar profile (**Account**) → **Login**. **K:** top-bar profile → **Sign in**. Follow `support_login.md` for the platform's actual sign-in/channel-selection flow; do not prescribe a browser callback or the same picker on every platform. Signed-in menus offer **Switch YouTube channel** if the wrong channel is active. [A1, K1]
2. **A/M:** open **Library** → **Playlists**. The profile popup's **Auto-sync with account** is on by default; leave it on for account synchronization. Alternatively, profile → tap the signed-in account name → **Playlists** browses the YouTube Music playlist library directly. **K:** open **Library** → **Playlists**. These read the account's playlist library, including saved playlists, rather than requiring every playlist link to be pasted. [A1–A2, K2]
3. If refreshing is needed: **A/M**, check the profile popup's **Auto-sync with account**, then reopen **Library** → **Playlists** (entry queues a playlist sync). **K Android/iOS**, pull down in Library; **K desktop**, use the top-bar **Refresh** while Library is open. Network/account access is required; an empty result does not establish that login or playlists are unsupported. [A2, A4, K2]

Stop here for a simple “get my YouTube Music playlists into Metrolist” request. Import/copy and editing details below are optional.

## Save, copy, or sync?

### Standalone Android (A/M)

- **Save to library:** the online playlist's heart saves a remote-linked library entry, not an independent copy. Its toggle also sends a YouTube playlist like/unlike request. Saving somebody else's playlist does not grant editing rights. [A3, A5]
- **Copy songs manually:** open the online playlist → its three-dot menu → **Add to playlist** → choose a destination. For a new local copy, choose **Create playlist**, name it, leave **Sync playlist** off, confirm, then select that new playlist in the destination list. Creating it alone does not add the songs. A local destination has no link back to the source playlist, so later source edits are not tracked. A linked destination can send additions to YouTube Music. [A3–A4]
- Do **not** invent an online-playlist menu item named **Import playlist**: A/M contain that dialog/label, but the traced menu never sets its visibility to true. The reachable copy action is **Add to playlist**. [A3]
- **Sync playlist** in **Create playlist** is a creation choice, initially off. Turning it on requires login, connectivity and account sync enabled; it attempts to create a new YouTube Music playlist. The dialog warns the choice cannot be changed later. Failed remote creation can leave a local playlist with **YouTube sync failed. Playlist created locally.** This is not a switch to convert/import an existing playlist. [A3–A4]
- For a stored playlist with a remote link, its detail-screen three-dot menu offers **Sync**. That action reads remote songs into the local playlist; it is not an upload button. [A4–A5]

### KMP manual link import (K only)

The playlist heart saves a remote-linked entry and, when signed in for playlist management, sends save/unsave to YouTube Music; it is not a copy. [K6]

Manual import copies tracks to a chosen destination, without ongoing synchronization with the original source playlist. It is unnecessary just to view account playlists. [K3–K5]

| Platform | Verified manual steps |
| --- | --- |
| Android / desktop | **Library** → **New playlist** (+) → **Import playlist** opens **Backup & import**. Under the **Playlist imports** heading choose **YouTube Music**, paste the playlist link, then **Start Import**. After loading, choose/name a destination and **Start Import** again. Desktop's Library shortcut is shown only with playlist-management login available. [K3] |
| iOS | Profile → **Settings** → **Backup & import** → **Import playlist**. Paste into **Playlist URL or ID** → **Load playlist**, choose/name a destination → **Start Import**. Do not substitute the Android/desktop provider menu here. [K4] |

- **Local** creates a local copy. Android/desktop's **YouTube Music** destination, or iOS's **Create a new YouTube Music playlist**, creates a new remote playlist and adds the tracks; this needs sign-in. Selecting an existing editable playlist appends missing songs there, potentially on YouTube Music. **Favorites** changes likes, not a neutral playlist copy. [K3–K5]
- The Android/desktop source description advertises **public** YouTube/YouTube Music links. Private-link import availability is unknown; prefer the signed-in Library route for private account playlists. Do not ask users to make a private playlist public. Repeated song IDs are deduplicated during manual import; partial imports can leave a partly populated destination. Check the result before retrying. [K3, K5]

## Direction and limits

- **A/M account auto-sync:** pulls the saved-playlist list and songs from YouTube Music. Home startup/refresh also requests a full sync, subject to a 30-minute automatic cooldown; entering Library → Playlists has its own queued sync. This is not continuous background mirroring. **Auto-sync with account** is not a read-only/offline safety switch: separate heart/edit/delete/add handlers can still send remote requests. [A2–A6]
- **A versus M:** A preserves local song occurrences missing from the remote list and skips an empty remote playlist, so remote removals need not disappear locally. M drops previously confirmed remote occurrences missing from the new response, preserves pending local additions, and accepts an empty playlist only when the response advertises zero songs. Do not promise identical deletion propagation or lossless bidirectional sync. [A4]
- **K:** Library refresh reconciles account membership/metadata; known remote-library removals need two complete refreshes. Opening a remote playlist loads its tracks and can replace its local cache. This is different from copying/importing into a separate destination. Do not promise every stored playlist's tracks are continuously refreshed. No Android-style **Auto-sync with account** playlist toggle was verified in K; its `ytmSync` setting is used by listening-history code, not these Library refresh paths. [K2, K6]
- **Owned versus others:** editing follows the source's editability flag, not merely presence in Library. K's **Owned** filter selects editable/local playlists; **Playlists** includes saved others' playlists. On editable remote-linked playlists, add/remove/rename/reorder actions can modify YouTube Music. Saving/copying another author's playlist does not let the user edit that author's original. [A3–A5, K2, K6]
- **Completeness:** A/M's completed playlist/library fetches allow at most 50 continuation requests, not “50 songs.” Manual **Add to playlist** uses the supplied song list when nonempty, which can still be loading or filtered; do not promise a complete large-playlist copy. K link imports deduplicate song IDs. No universal song-count limit, exact sync latency, or guaranteed private/region-restricted/unavailable-track import was verified. Library entries/cached metadata are not downloaded audio. [A3, A6, K5]

## Data-loss / privacy boundary

Deleting a remote-linked owned playlist can delete it on YouTube Music too; renaming/removing songs can likewise affect the account. Do not use **Delete**, logout **Clear**, reinstalling, or clearing app data as a playlist-refresh fix. Preserve/back up local-only playlists before destructive actions. K Android/desktop distinguish deleting an editable playlist from removing another author's saved playlist from the library. [A5, K6; logout details in `support_login.md`]

Never request passwords, cookies, tokens, HARs, private playlist links, or credential-bearing screenshots/logs in chat, DMs or bot tools. Link entry and account authentication belong in the user's own app. A local copy is not an upload; selecting a YouTube Music destination does write to the account. Do not assume the resulting remote playlist's visibility without checking it.

## Immutable evidence map

All paths are relative to the named repositories at revisions A/M/K above, not mutable working-tree files. Prefixes:

- `a/` = `app/src/main/kotlin/com/metrolist/music/`
- `ka/` = `androidApp/src/main/kotlin/com/metrolist/android/`
- `d/` = `desktopApp/src/jvmMain/kotlin/com/metrolist/desktop/`
- `s/` = `shared/src/commonMain/kotlin/com/metrolist/shared/`
- `i/` = `shared/src/iosMain/kotlin/com/metrolist/shared/`

[A1] A/M: `a/MainActivity.kt`, `a/ui/component/Dialog.kt`, `a/ui/screens/settings/AccountSettings.kt`, `a/ui/screens/LoginScreen.kt` (entry, account-name action, toggle/channel).
[A2] A/M: `a/ui/screens/AccountScreen.kt`, `a/viewmodels/AccountViewModel.kt`, `a/ui/screens/library/LibraryScreen.kt`, `a/ui/screens/library/LibraryPlaylistsScreen.kt`, `a/viewmodels/PlaylistsViewModel.kt` (playlist library and queued refresh).
[A3] A/M: `a/ui/screens/playlist/OnlinePlaylistScreen.kt`, `a/ui/menu/YouTubePlaylistMenu.kt`, `a/ui/menu/ImportPlaylistDialog.kt`, `a/ui/menu/AddToPlaylistDialog.kt`, `a/ui/component/CreatePlaylistDialog.kt` (reachable save/copy/create controls; M's create-dialog changes are layout-only).
[A4] A/M: `a/utils/SyncUtils.kt` (`executeSyncSavedPlaylists`, `executeSyncPlaylist`, `createPlaylist`, scheduled edits; M's `preservedLocalSongs`, `isGenuineEmptyPlaylist`).
[A5] A/M: `a/db/entities/PlaylistEntity.kt`, `a/ui/screens/playlist/LocalPlaylistScreen.kt`, `a/ui/menu/PlaylistScreenMenus.kt`, `a/ui/menu/PlaylistMenu.kt` (remote bookmark/edit/delete and manual Sync callers).
[A6] A/M: `a/viewmodels/HomeViewModel.kt`, `a/extensions/ContextExt.kt`, `a/constants/PreferenceKeys.kt`, `a/viewmodels/OnlinePlaylistViewModel.kt`, `innertube/src/main/kotlin/com/metrolist/innertube/utils/Utils.kt`. Labels: `app/src/main/res/values/strings.xml`, `app/src/main/res/values/metrolist_strings.xml`.
[K1] K: `s/ui/components/AccountMenu.kt`, `s/ui/components/AccountPopupDialog.kt` (sign-in/switch entries; platform handoffs documented in `support_login.md`).
[K2] K: `ka/ui/screens/LibraryScreen.kt`, `d/ui/screens/LibraryScreen.kt`, `s/ui/mobile/MobileLibrary.kt`, `s/ui/screens/LibraryContent.kt`, `s/ui/screens/ScreenContentText.kt`, `s/viewmodels/LibraryViewModel.kt`, `s/api/YouTubeRepository.kt`, `s/db/PlaylistSync.kt`, `d/ui/layouts/AppLayouts.kt`, `d/ui/components/TopBar.kt`, `d/state/content/LibraryContentState.kt` (filters, refresh callers, account fetch, cache/reconciliation).
[K3] K: `ka/ui/MetrolistApp.kt`, `ka/ui/screens/settings/PlaylistImportScreen.kt`, `ka/ui/screens/settings/BackupRestoreScreen.kt`, `d/ui/screens/settings/SettingsOverlay.kt`, `d/ui/screens/settings/BackupRestoreSettings.kt` (Library shortcut routes, provider inputs and destinations).
[K4] K: `s/ui/mobile/MobileApp.kt`, `s/ui/settings/MetrolistSettingsRegistry.kt`, `i/MainViewController.kt`, `i/ui/settings/IosPlaylistImportSettings.kt` (iOS Settings entry, input/destination flow).
[K5] K: `s/playlistimport/PlaylistImportManager.kt`, `s/playlistimport/UnifiedPlaylistImporter.kt`, `s/api/YouTubePlaylistEdits.kt` (one-time copy, deduplication, partial results, remote writes).
[K6] K: `s/playlist/PlaylistMutations.kt`, `ka/ui/screens/PlaylistScreen.kt`, `ka/viewmodels/PlaylistViewModel.kt`, `d/ui/screens/PlaylistScreen.kt`, `d/state/content/PlaylistAlbumState.kt`, `s/ui/mobile/MobileDetailScreens.kt`, `s/settings/Settings.kt`, `s/api/YouTubeHistoryOperations.kt`. K labels: `shared/src/commonMain/composeResources/values/strings.xml`.

## Verification limits

Source/resources/callers checked read-only at the pinned commits. Not hardware/account-tested, not packaged-app or KMP release proof. Google/YouTube responses, permissions, network/region behavior and successful imports remain unverified. If UI differs, obtain variant/version/platform and a non-secret symptom; state unknown rather than invent a menu or a universal sync guarantee.
