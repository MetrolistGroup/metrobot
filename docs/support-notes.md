# Support note maintenance

`support-notes.json` contains authored replacements for technical notes, not an automatic seed or a production database export. Validate with `go test ./cmd -run TestAuthoredSupportNotesAreValid`. Deploy only after comparing against a fresh live snapshot and backing up production. Apply changed rows in one transaction; never replace the live database with a local copy. Keep private snapshots and receipts outside Git.

## Source review

Reviewed all 18 saved notes against these sources on 26 October 2026:

- `MetrolistGroup/Metrolist-KMP@067d407c385d748c7716d54007006f9db53b4ead`: `androidApp/build.gradle.kts`, `shared/build.gradle.kts`, `iosApp/Configuration/{Config.xcconfig,Info.plist}`, README, `docs/{development,desktop,releasing}.md` and reusable platform/tester release workflows. Android minimum API 28, target API 37, universal APK and separate release application ID; iOS 17 ARM64; documented macOS 15 minimum and Intel/Apple Silicon packaging; Windows x64; Linux x64/ARM64 AppImages. README download badges alone are not evidence that an asset is published.
- KMP diagnostics: shared `ui/screens/AboutScreen.kt`, `ui/settings/PlaybackEventLogSection.kt`, `ui/mobile/MobileApp.kt`, Android/desktop `PlaybackEventLogSection.kt` and desktop `utils/AppLogger.kt`. Three logo taps unlock Devtools; current shared export button is **Share**, iOS exposes **Copy**. Desktop export is saved to the log directory.
- KMP migration: shared `backup/{BackupFormat,LegacyBackupConversion,LegacyPreferencesProto}.kt`, platform backup/restore callers and desktop Spotify import. Restoration can replace destination data; do not promise migration of every platform setting, credential or audio file.
- `MetrolistGroup/Metrolist@876e355181fd3b04e7063b10442fef1b721d53b3` and stable `v13.7.0` (`7c5ba3ad22cb562d337897f82163a2b7fce665f3`): `app/build.gradle.kts`, song menu, Player/Storage settings and release/nightly workflows. Standalone minimum API 26; FOSS/GMS/Izzy capabilities are distinct from KMP. Sibling checkout has unrelated local workflow/toolchain edits, which were neither changed nor treated as published behavior.
- Live public GitHub release metadata confirms stable v13.7.0 APK names and the standalone nightly Google Cast asset. LogFox/Shizuku release-page links replace old version-pinned APK links.
- RPC setup/privacy wording uses the immutable source scopes in `discord/garmin_skills/support_rpc.md`; no claim that OAuth is risk-free or local logout revokes remote authorization.
- `metrosite2` branding/resources and both app repositories agree on the official website and donation destinations. Source is stronger evidence than stale website/download copy.

## Inventory decisions

| Notes | Decision |
| --- | --- |
| `kmp` | Add OS versions, chip/package choices, IPA signing and Android build details; remove unsupported Windows ARM64 and blanket Linux distribution promises. Preserve existing tester eligibility and no-date policy. |
| `download` | Replace the expiring Discord video URL with source-verified standalone song-download instructions. |
| `logs`, `playbackkmp` | Separate Android logcat from KMP playback events, correct export controls and add privacy/reproduction guidance. |
| `nightly`, `up`, `ver` | Distinguish standalone and KMP, document verified minima/flavors, avoid pinned stable download URLs and unsafe uninstall advice. |
| `playback`, `rpc` | Remove blanket fix/privacy promises and routine logout advice; retain scoped, low-risk checks and actual setup. |
| `repo` | Identify public standalone source without promising KMP source access. |
| `appname`, `whatsnightly` | Add invocation descriptions; retain the name origin and clarify nightly's limits. |
| `dehoist` | Existing behavior/description matches nickname normalization; retain. |
| `donate` | Destinations match project resources; preserve randomized markup and existing invocation description. |
| `site` | Official address and invocation description remain valid; retain. |
| `testertour` | Preserve staff-managed access/reporting policy and existing description. Private channel membership and staff grants are not inferable from app source. |
| `femboy`, `fixit` | Joke/media notes, not technical instructions. Preserve content and blank routing descriptions so they remain manual-only. |

## Selective context

Note short descriptions describe **when to invoke**, including important exclusions. Routing sees names/descriptions, not every body; `get_note` retrieves the selected content. Blank descriptions exclude joke/media notes from automatic support. `load_skill` advertises two short scopes and returns only the selected skill. `load_support_guide` advertises four distinct scopes and retrieves one guide, without automatically bundling the others. The small dedicated support core now includes the scoped project reference and minimal verified standalone login/library steps, not all setup guides. With Decisions configured, casual app-fact requests and short corrections use the isolated support path too; ordinary chat does not load full support evidence. The independent support evidence checker deliberately retains all four trusted guides; selective answering context must not weaken that safety check.

Source review does not establish device compatibility, successful account login, or the quality of every model answer. Private model replays are small samples: unsafe drafts can still be generated and rejected, valid answers can be suppressed, and semantic routing is probabilistic. Classifier outages stop casual generation rather than risk unchecked app guidance. Tester channels/access policies were preserved, not independently reauthorized.
