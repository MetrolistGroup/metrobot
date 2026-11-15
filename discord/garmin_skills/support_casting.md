# Casting support reference

Scope: Metrolist v14 on Android 7+, iOS 17+, Windows 10+, macOS 15+ and Linux unless stated otherwise. The KMP rewrite is now Metrolist in the public MetrolistGroup/Metrolist repository. Menu details below come from pre-v14 source review, not v14 device tests, and may differ. Ask for platform/version and the visible label if needed.

Casting to Google Cast, F-Cast, SONOS and DLNA is supported on Android and desktop. Google Cast is built into the universal APK; there is no GMS-only requirement. Do not infer iOS casting support from that list or assume all Wi-Fi receivers support the same protocol.

For a vague Wi-Fi question, explain the available protocols and ask which receiver/protocol and stage fails: discovery, connection or playback. Android 13.x used Settings > Player > Google Cast and the player's Cast button. That path may differ in v14; do not present it as a verified v14 setting or invent a generic streaming menu.

Check current official issues for a matching problem. An old queue-transition crash fix does not prove all casting works. For a matching published nightly fix, compare nightly with stable and inspect the patch. Offer a backed-up, unstable in-place trial only on a platform with a published nightly asset.

Never request passwords, cookies, tokens, callback URLs, HARs, backups or credential-bearing logs/screenshots in chat or tools. Credentials belong only in the app. Never prescribe reinstalling, clearing app data or logout as routine troubleshooting. Back up before a source-supported destructive action.
