# Metrolist Google / YouTube Music login

Scope: Metrolist v14 on Android 7+, iOS 17+, Windows 10+, macOS 15+ and Linux unless stated otherwise. The KMP rewrite is now Metrolist in the public MetrolistGroup/Metrolist repository. Menu details below come from pre-v14 source review, not v14 device tests, and may differ. Ask for platform/version and the visible label if needed.

Music-account login is separate from Discord authorization. Start with the profile/account menu and Sign in, then follow the platform's own flow. Do not invent Settings > Account.

Pre-v14 rewrite flows, labels may differ:
- Android: Sign in to YouTube Music opens an in-app WebView. Complete Google verification there.
- iOS: sign-in uses WKWebView.
- Linux/macOS: native WebView. Complete sign-in can retry completion after reaching YouTube Music. Missing Linux WebKit can make sign-in unavailable; consult current install docs rather than invent dependencies or fixes.
- Windows: Windows login helper opens a separate WebView2 window. Start login process is available when it is not running. The helper returns the session automatically; do not append a channel-picker step to initial Windows login.

On Android/iOS/non-Windows desktop, Choose a YouTube channel and Continue appeared only when multiple channels were found. Switch YouTube channel uses the existing session; it is not a saved-Google-account switcher. Another login can require an intentional logout, which is not a repair step. Rewrite logout had confirmation, not Android 13.x Keep/Clear. If cleanup fails, do not claim credentials were removed.

Historical Android 13.x: Home > profile Account > Login opened an in-app WebView; successful initial login restarted the app. Log out offered Keep library data? Keep preserved the library, Clear removed local library/playlists/history. Both cleared credentials and cookies. These 13.x choices may differ in v14 and never justify routine logout.

Prefer ordinary sign-in over Advanced sign-in/Log in with token. Never teach credential extraction. Google challenges, regional behavior and successful authentication remain unverified for a user's device.

Never request passwords, cookies, tokens, callback URLs, HARs, backups or credential-bearing logs/screenshots in chat or tools. Credentials belong only in the app. Never prescribe reinstalling, clearing app data or logout as routine troubleshooting. Back up before a source-supported destructive action.
