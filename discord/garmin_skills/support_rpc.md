# Discord Rich Presence (RPC) setup reference

Use for setup/how-to questions. Match product, version and platform first. Labels are English; translations may differ.

## Verified scopes

- **A: standalone Android v13.7.0**, tag commit `7c5ba3ad22cb562d337897f82163a2b7fce665f3`.
- **B: standalone Android main source**, `6d08504262de76a4ba841dc24489a754e2f48e4f`; same menu/login/switch flow as A.
- **C: KMP source**, `2ac8e47c5566b5771133d798618d75cc69c2d039` (checkout verified equal to origin/main); Android, desktop and iOS integration callers.

Source-verified, not device-tested. B/C do not prove availability in distributed releases. Do not apply C's labels/lifecycle to A/B or call current source a released fix.

## Setup shared by the verified variants

For an unspecified variant, these steps are sufficient:
1. Open the **account/profile menu**, choose **Integrations**, then its Discord entry.
2. Choose **Log in** on that Discord page and complete Discord authorization in the flow Metrolist opens.
3. Turn on that page's Rich Presence switch, then play music.

Do not add an Android-only label or an external-browser requirement to this shared flow. Integrations is not in the main Settings list. Google/YouTube **Login/Sign in** is a separate flow, not a prerequisite for opening Integrations or connecting Discord.

Use the exact labels below only for the stated variant:

| Label/default | Standalone Android A/B | KMP C |
| --- | --- | --- |
| Discord entry | **Discord Integration** | **Discord** |
| Enable switch | **Enable Rich Presence**, under **Options** | **Enable Discord Rich Presence**, under **Rich presence** |
| Unset enable preference | On | Off |

Both switches require successful Discord login.

## Standalone Android: A and B

Authorization opens in a browser/Custom Tab. Sign in to Discord there if needed, approve authorization and return to Metrolist through the callback.

- Stop sharing: turn **Enable Rich Presence** off. The service disconnects RPC when ready; login remains saved. **Log out** on the Discord card disconnects and clears saved Discord authorization.
- Pausing can retain song metadata without running timestamps. The service schedules disconnect after about one minute paused with the screen on, or ten minutes idle with it off, if still not playing and RPC is ready. Service teardown also disconnects. These are source timers, not guaranteed Discord display deadlines.
- **Advanced mode** is optional for setup. It exposes **Activity type**, **Activity name**, **State**, **Details**, button and status controls. Basic mode already supplies song/artist details.

## KMP: C only

The shared setup applies on Android, desktop and iOS. **Log in** is under the **Account** heading on the Discord page; **Account** is not an extra button. Use C's labels from the table above and the platform authorization flow below.

Platform authorization differences:
- **Android:** tries the Discord app first; if no handler exists, falls back to a browser Custom Tab or browser. The callback is handled by Metrolist.
- **Desktop:** uses an embedded authorization window; it intercepts the callback and closes after successful exchange. Webview availability/writable storage can block sign-in; no universal remedy is verified.
- **iOS:** opens HTTPS authorization via the system URL opener and handles the app callback; this caller makes no explicit app-first attempt.

- Stop sharing: turn **Enable Discord Rich Presence** off. The manager attempts to clear presence before suspending its connection; login remains. To remove saved authorization, use the **Logout** account row and confirm.
- Unlike A/B, C clears presence when playback is paused or there is no current song. iOS explicitly attempts to keep RPC connected during background playback and disconnects when backgrounded without playback; OS suspension and actual background reliability are unverified.
- **Advanced mode** exposes activity/text/button customization. **Refresh RPC** re-submits presence, requiring login, the enable switch, a current track and a ready manager. It is not a verified fix.

## Answer boundaries

The app handles OAuth and publishes via Discord's Gateway, not user-configured desktop IPC. Never request passwords, tokens, callback URLs or HARs in chat, or explain token extraction. Local logout is not verified server-side revocation. Preview/status is not proof of visibility to others. Discord privacy menus, concurrent-client behavior, policy safety and missing-presence fixes are unverified. Do not invent steps or recommend clearing data/reinstalling.

## Source map

All paths are relative to the named app repository at the immutable revisions above. Prefixes and brace lists below expand to individual paths.

**A/B, Metrolist:** `M = app/src/main/kotlin/com/metrolist/music`.
- Entry: `M/MainActivity.kt`, `M/ui/component/Dialog.kt`, `M/ui/screens/NavigationBuilder.kt`, `M/ui/screens/settings/AccountSettings.kt`, `M/ui/screens/settings/integrations/{IntegrationScreen,DiscordSettings}.kt`.
- Auth/state/lifecycle: `M/discord/{DiscordAuth,DiscordOAuthActivity,DiscordRpcManager,DiscordGateway,DiscordActivityBuilder}.kt`, `M/constants/PreferenceKeys.kt`, `M/playback/MusicService.kt`; labels: `app/src/main/res/values/{strings,metrolist_strings}.xml`; callback: `app/src/main/AndroidManifest.xml`.

**C, Metrolist-KMP:** `S = shared/src/commonMain/kotlin/com/metrolist/shared`, `N = androidApp/src/main/kotlin/com/metrolist/android`, `D = desktopApp/src/jvmMain/kotlin/com/metrolist/desktop`, `I = shared/src/iosMain/kotlin/com/metrolist/shared`.
- Menus/settings: `S/ui/components/AccountMenu.kt`, `S/ui/settings/SharedIntegrationsScreen.kt`, `N/ui/MetrolistApp.kt`, `N/ui/screens/settings/IntegrationsScreen.kt`, `D/ui/{components/TopBar,layouts/AccountMenu,screens/settings/IntegrationsOverlay}.kt`, `S/ui/mobile/{MobileApp,MobileAccountWorkflow}.kt`.
- Auth/state: `S/discord/{DiscordAuth,DiscordRpcManager,DiscordGateway,DiscordPlaybackSession}.kt`, `S/settings/Settings.kt`, `D/ui/screens/DiscordLoginDialog.kt`, `D/auth/DiscordLoginNavigation.kt`, `shared/src/androidMain/kotlin/com/metrolist/shared/discord/DiscordUrlOpener.android.kt`, `I/utils/PlatformUtils.ios.kt`.
- Playback/labels: `N/playback/MusicService.kt`, `D/state/playback/DiscordRpcState.kt`, `I/discord/IosDiscordPresenceEffect.kt`; `shared/src/commonMain/composeResources/values/strings.xml`.
