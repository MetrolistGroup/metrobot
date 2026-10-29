# Playback support reference

Scope: standalone Android; do not apply its menus/APKs to KMP. Source-reviewed 28 October 2026, not device-tested.

## Start with useful checks

Read `get_note(name=playback)`. For a first report of Source error, buffering or songs stuck at 0:00, give one or two applicable checks from it: another song, YouTube Music on the same connection, force-close/reopen, or another audio-quality setting. These are diagnostic checks, not a diagnosis or guaranteed cure. Ask one missing detail, such as the exact error or app version, only if it changes the next step.

If the user already said downloaded songs work, do not ask again. That narrows the symptom to online playback; it does not establish a network, account or extractor cause. Do not make the user wait for a release-history investigation before giving these basic checks.

## When the checks have failed or the user asks about a fix

Search relevant official issues and read the matching report and maintainer comments. For a possible post-release fix, use `get_metrolist_changes(ref=nightly)` with one short topic keyword, then inspect a relevant returned SHA with `get_metrolist_commit`. Do not repeat the same search or comparison. Issue closure, a dependency bump or an unrelated exception does not prove a fix for this person's error.

After verifying the published nightly and relevant patch, a safe trial is:

"If you're on standalone Android stable, back up your library/settings, then install the current nightly over your existing app. Do not uninstall. It includes newer playback code, but this is a trial, not a confirmed fix for your error. Nightly may be unstable."

Use the actual returned APK link and describe only the inspected change. Never invent backup menus or guarantee success. If research does not establish a fix, ask for one reproducible example or missing error/version detail rather than inventing a cause.

Never prescribe uninstall/reinstall, clearing app data, deleting downloads or logout as routine troubleshooting. If Android rejects an in-place update, ask for its exact installer error.
