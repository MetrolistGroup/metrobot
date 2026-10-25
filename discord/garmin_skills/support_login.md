# Metrolist Google / YouTube Music login

Match variant/version/platform first. English labels. Music-account login, not Discord/import login.

## Source applicability

- **A: standalone Android v13.7.0**, tag `7c5ba3ad22cb562d337897f82163a2b7fce665f3` in `/mnt/work/projects/Metrolist`.
- **M: standalone Android main**, HEAD/main `6d08504262de76a4ba841dc24489a754e2f48e4f` (`6d0850426`), not the stable tag. Traced login flow matches A.
- **K: KMP**, `/mnt/work/projects/Metrolist-KMP` HEAD equals local `origin/main`: `2ac8e47c5566b5771133d798618d75cc69c2d039`. Snapshot only, not remote freshness or proof of features/access in a user's release.

## Standalone Android (A; also traced in M)

1. On **Home**, tap the top-bar profile icon (**Account**), then **Login** while signed out. Not “Settings > Account”. [A1]
2. **Login** opens Google sign-in in an **in-app Android WebView**, continuing to `https://music.youtube.com`. Complete Google's sign-in/verification there. HTTP(S) stays in the view; `intent:` links may open another app. This is not a default-browser OAuth callback flow. [A2]
3. If **Choose a YouTube channel** appears, select the intended channel and **Continue**. It appears for multiple discovered channels; otherwise login proceeds. Successful validation/save restarts the app. [A2]
4. Signed in: **Switch YouTube channel** reuses the session; it is not a saved-Google-account switcher. [A1–A2]
5. **Log out** opens **Keep library data?**: **Keep** signs out without clearing the library; **Clear** removes local library/playlists/history while retaining downloaded songs. Back up before intentionally choosing Clear; never prescribe it as login repair. Both sign-out paths clear app credentials/WebView cookies. [A1, A3]

## KMP (K snapshot only)

Top-bar profile button → **Sign in** while signed out (mobile popup/desktop menu). [K1–K4]

| Platform | Ordinary sign-in handoff |
| --- | --- |
| Android | **Sign in to YouTube Music**: in-app Android WebView, Google sign-in → YouTube Music; no system-browser callback. Initial login relaunches the app; channel switching returns. TV's **Sign in on TV** warns a keyboard/mouse may be needed. [K2] |
| iOS | **Sign in to YouTube Music**: **WKWebView**, Google sign-in → YouTube Music. New login clears its cookies; switching preserves them. Completion returns and refreshes the app. [K3] |
| Linux/macOS desktop | **Sign In - YouTube Music**: native WebView, Google sign-in → YouTube Music. Automatic completion; **Complete sign-in** retries completion if the view remains after reaching Music. Failed Linux WebKit detection shows **YouTube sign-in unavailable** / **Channel switch unavailable**. [K4] |
| Windows desktop | **Windows Login Helper** auto-launches a separate **WebView2 helper window**, not the default browser. Sign in there; session returns automatically. **Start Login Process** is available when it is not running; missing/failed helpers show errors. [K4] |

- Android/iOS/non-Windows desktop show **Choose a YouTube channel** when multiple channels are discovered: select one and **Continue**. Signed-in menus offer **Switch YouTube channel**. Windows switching lists only channels usable from the existing session; **Start Login Process** starts fresh helper login. Do not promise every channel is listed. [K1–K4]
- For another Google login, the verified entry is **Sign in** after **Log out** and confirmation. No Keep/Clear choice here. Logout clears app credentials/web-session state, not Google sign-in elsewhere. Desktop can report **Signed out, but the WebView login profile could not be removed.** with **Retry cleanup**; do not claim cleanup succeeded. [K1, K5]

## Advanced mode / privacy

A has **Log in with token**; K has **Login with token** → **Advanced sign-in** (iOS/TV also expose **Token login**). These edit local session credentials; prefer ordinary sign-in. [A1, K1–K4, K6]

Tokens/cookies can grant account access. Never request passwords, cookies, tokens, HARs or credential-bearing screenshots/logs in chat, DMs or bot tools. Advanced credential entry belongs only in the user's own app. No extraction techniques or credentials sent to the bot.

## Immutable evidence map

Use revisions A/M/K above; paths below use literal prefixes:

- `a/` = `app/src/main/kotlin/com/metrolist/music/`
- `ka/` = `androidApp/src/main/kotlin/com/metrolist/android/`
- `d/` = `desktopApp/src/jvmMain/kotlin/com/metrolist/desktop/`
- `s/` = `shared/src/commonMain/kotlin/com/metrolist/shared/`
- `i/` = `shared/src/iosMain/kotlin/com/metrolist/shared/`

[A1] `a/MainActivity.kt`, `a/ui/component/Dialog.kt`, `a/ui/screens/settings/AccountSettings.kt`, `a/ui/screens/NavigationBuilder.kt` (entry/routes).
[A2] `a/ui/screens/LoginScreen.kt` (WebView/picker/restart).
[A3] `a/viewmodels/AccountSettingsViewModel.kt`, `a/App.kt` (`forgetAccount`), `a/utils/SyncUtils.kt` (`executeClearAllLibraryData`). A labels: `app/src/main/res/values/{strings,metrolist_strings}.xml`.
[K1] `s/ui/components/{AccountMenu,AccountPopupDialog}.kt` (labels/actions/confirmation).
[K2] `ka/ui/MetrolistApp.kt`, `ka/ui/components/AccountMenu.kt`, `ka/ui/screens/LoginScreen.kt`.
[K3] `s/ui/mobile/MobileApp.kt`, `i/MainViewController.kt`, `i/ui/mobile/IosLoginScreen.kt`.
[K4] `d/ui/components/TopBar.kt`, `d/ui/layouts/AccountMenu.kt`, `d/Main.kt`, `d/ui/screens/LoginDialog.kt`, `d/ui/components/WebViewUnavailableContent.kt`, `metro-wv2-helper/Program.cs`.
[K5] `s/auth/{AuthManager,YouTubeSession}.kt`, `s/ui/mobile/MobileAccountWorkflow.kt`, `d/state/auth/AuthState.kt`; `shared/src/{androidMain,desktopMain,iosMain}/kotlin/com/metrolist/shared/utils/WebCookieUtils.*.kt`.
[K6] `s/ui/TokenLoginScreen.kt`, `d/ui/components/TokenLoginDialog.kt`. K labels: `shared/src/commonMain/composeResources/values/strings.xml`.

## Limits

Source/callers/resources checked, not live login or packaged apps/devices. Google labels/challenges, regional/network behavior and login success remain unverified. If UI differs, obtain variant/version/platform and a non-secret error. No blanket data clearing/reinstalling or unverified fixes.
