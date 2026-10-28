# Casting support reference

Scope: standalone Android v13.7.0, tag `7c5ba3ad22cb562d337897f82163a2b7fce665f3`. Source-reviewed 28 October 2026, not device-tested.

- Wi-Fi audio may mean different protocols. Start "If you mean Google Cast on standalone Android...", not a universal claim.
- Short answer for a vague Wi-Fi question: "If you mean Google Cast on standalone Android, it requires the GMS build. Enable Settings → Player → Google Cast and use the player's Cast button. Is the receiving device missing, or does playback fail after connecting?" Give this scoped answer instead of first making the user identify their build.
- Google Cast requires the GMS APK (`Metrolist-with-Google-Cast.apk`). Standard/FOSS and Izzy have no Cast. In GMS, Settings → Player → Google Cast enables the player's Cast device chooser; the switch defaults on. Ask which receiving device/stage fails: discovery, connection or playback. Do not invent a generic streaming menu or assume all Wi-Fi devices are compatible.
- Provenance: `app/build.gradle.kts` flavor flags, `app/src/main/kotlin/com/metrolist/music/ui/screens/settings/PlayerSettings.kt`, `app/src/gms/kotlin/com/metrolist/music/ui/component/CastButton.kt` at that tag. Do not apply these flavors/menus to KMP.
- Issue index: https://github.com/MetrolistGroup/Metrolist/issues/4359. Read current comments: maintainers describe a queue-transition crash fix, while later users separately report stops/desynchronization. That crash fix does not prove all casting is fixed. For matching crashes inspect `get_metrolist_changes(ref=nightly,query=cast)` and the relevant patch; offer a matching published nightly only as a backed-up, in-place trial with an instability warning.
