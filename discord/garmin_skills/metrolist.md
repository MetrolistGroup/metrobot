# Metrolist project reference

Facts verified 15 November 2026 from the public README, install docs and releases. Metrolist v14.0.0 shipped 14 November 2026. The KMP rewrite is now Metrolist, free and public with one repository, https://github.com/MetrolistGroup/Metrolist. No donation, tester role or source-access gate remains. Use live status tools for the current release or activity; search official issues before claiming a report or fix exists.

Android v14 uses one universal APK with Google Cast and updates 13.x in place as `com.metrolist.music`; back up first. Login is not a KMP-only feature. Use live tools for versions/fixes. Use focused notes/guides for packages, OS minimums and setup; 13.x paths may differ in v14.

## On-demand platform and feature details

- Android 7+ (API 24): universal `Metrolist.apk`, app ID `com.metrolist.music`, with Google Cast built in.
- iOS 17+: `Metrolist.ipa`, signed with a sideloading tool; `metrolist-altstore.json` is also available.
- Windows 10+: x64 installer or portable zip, ARM64 portable zip.
- macOS 15+: Apple silicon or Intel DMG. The app is unsigned, so trust it before opening.
- Linux: x64 or ARM64 AppImage; the community AUR package is `metrolist-desktop-bin`.
There are no MSI, DEB, RPM or Flatpak downloads.

Android v14 updates 13.x in place with `com.metrolist.music`. Back up first; do not uninstall or clear data. Former FOSS/GMS flavours merged into `Metrolist.apk` with Google Cast. The legacy `Metrolist-with-Google-Cast.apk` is the same app. IzzyOnDroid only omits the updater: https://apt.izzysoft.de/fdroid/index/apk/com.metrolist.music.

Nightly is the rolling public prerelease at https://github.com/MetrolistGroup/Metrolist/releases/tag/nightly, rebuilt on every main push. Android `Metrolist.apk`, Linux x64 version+sha7 AppImage and Windows x64 `Metrolist-Desktop-windows.zip` are published. Back up before the unstable in-place trial; never promise a fix. Read uploaded assets for exact names. Stable assets include `Metrolist.apk`, `Metrolist.ipa`, `metrolist-altstore.json`, `Metrolist-Desktop-windows.zip`, `metrolist-installer-windows-x64-VERSION.exe`, `metrolist-desktop-windows-arm64-VERSION.zip`, `metrolist-desktop-macos-arm64-VERSION.dmg`, `metrolist-desktop-macos-x64-VERSION.dmg`, `metrolist-desktop-linux-x64-VERSION.AppImage`, `metrolist-desktop-linux-arm64-VERSION.AppImage` and `SHA256SUMS`.

YouTube Music playback works with or without sign-in. Login is not a KMP-only feature. Metrolist has Material 3 Expressive, Discord Rich Presence, Last.fm/ListenBrainz, no ads, Listen Together, Google Cast/F-Cast/SONOS/DLNA on Android and desktop, backup/playlist imports from 30+ clients and 6 services, Shazam, translated/romanized lyrics, 15+ languages, video on Android and desktop, AI/explicit/SFW filters, Canvas, player/miniplayer/background styles, custom fonts and icons. Use support guides for setup; do not invent v14 menu paths from 13.x instructions or promise lossless two-way sync.

Official links: https://metrolist.cc, FAQ https://metrolist.cc/#faq, Discord https://dsc.gg/metrolist, Telegram https://t.me/metrolistapp. Install docs are `docs/install/{android,ios,windows,macos,linux}.md` in the public repository. Mostafa Alagamy created Metrolist; Nyx and Lamp created metrobot. Source and forks follow GPL-3.0.
