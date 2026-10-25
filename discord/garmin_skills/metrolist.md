# Metrolist project reference

Use this skill for questions about the Metrolist project, its status, downloads, or official resources.

- Standalone Metrolist is a free, open-source Android YouTube Music client built with Kotlin and Material 3. Metrolist-KMP is a separate multiplatform track: the pinned source implements Android, iOS and desktop (Windows/Linux/macOS). Source support is not proof that a build is publicly available; do not call the whole project strictly Android-only.
- Google/YouTube Music login and account playlists are supported in standalone Android v13.7.0 and the pinned KMP source. Login is not a KMP-only feature. These are verified scopes, not the versions that introduced login; do not say login is new or started in v13.7.0. Use `load_support_guide` with `name=login` for sign-in and `name=playlists` for account playlists/sync; call the tool rather than telling users its name. Use only verified menu paths and refresh steps, and do not promise automatic two-way synchronization.
- Standalone Metrolist's source is public. Do not generalize KMP tester or source-access requirements to standalone Metrolist, or claim KMP source is publicly accessible. Retrieve the current `kmp`/`donate` note for access guidance; build access does not by itself establish source access.
- Standalone Metrolist is in maintenance mode: bug fixes and minor improvements continue, but major feature proposals are generally not accepted. It is not abandoned. Do not apply this maintenance description to the separate KMP track.
- Official website: https://metrolist.cc
- Standalone repository and releases: https://github.com/MetrolistGroup/Metrolist
- KMP source reference (access may be restricted): https://github.com/MetrolistGroup/Metrolist-KMP
- Official Discord: https://dsc.gg/metrolist
- Official Telegram: https://t.me/metrolistapp
- Stable APKs are published on GitHub Releases. Nightly builds come from GitHub Actions.
- Use `get_metrolist_status` before stating a current version, release date, activity status, star count, or recent change.
- Use `search_metrolist_issues` before claiming a bug or feature has or has not been reported.
