# Playback support reference

Scope: standalone Android; do not apply its menus/APKs to KMP. Source-reviewed 28 October 2026, not device-tested.

## Short answer after verifying a published nightly and relevant patch

"If you're on standalone Android stable, back up your library/settings, then install the [current nightly](returned APK URL) over your existing app. Do not uninstall. It contains newer playback/extractor code, so this is a trial, not a confirmed fix for your error. Nightly is in development and may be unstable."

Use that answer's conditions and actual returned APK link. Do not prepend a suspected cause or add backup menu paths, account-refresh steps or a longer questionnaire. A dependency bump proves the dependency changed, not which errors it fixes. Do not turn "Source error" into a diagnosis, "matches a known issue fixed in nightly", or a promise that changing builds will work.

## Research and alternatives

- Call `get_note(name=playback)` directly, then `get_metrolist_changes(ref=nightly,query=playback)` (or `stream`, not the whole error sentence). Once relevant changes appear, read a returned full SHA with `get_metrolist_commit`; do not repeat comparisons/catalog searches. Read matching issue comments when needed.
- Report index: #4372, #4417 and #4471. These are reports, not proof of this user's cause. A maintainer in #4417 recommended a nightly trial after `5ceae5f4e709f1ea5042a984e11835ff344bdaca`; verify actual published-build inclusion. A fix for a specific underlying exception cannot be claimed to fix a generic Source error.
- Without a verified matching build, use the playback note's low-risk diagnostic checks: another song, YouTube Music on the same connection, force-close/reopen. These are checks, not remedies. Then ask one useful missing detail. Downloads working narrows the symptom to online playback, not a network/account/extractor diagnosis; do not ask again whether downloads work.
- Never prescribe uninstall/reinstall, clearing app data, deleting downloads or logout. If Android rejects an in-place update, ask for its exact installer error.
