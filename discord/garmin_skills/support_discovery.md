# Music discovery

Use for Discover playlists, Home recommendations and the missing "Your daily discover" section. This is separate from account-playlist synchronization.

## Standalone Android v13.7.0

Home has a section called **Your daily discover**. It shows recommended songs based on liked songs. It is a Home recommendation section, not a saved playlist in Library. Do not deny that discovery exists because there is no saved playlist with that exact name.

The app chooses up to five liked songs as seeds and requests related recommendations. The section appears only when recommendations are nonempty and no Home filter chip is active. With no liked songs, no related results, or an active filter chip, the section can be absent. This does not establish a network fault or an app bug. Do not invent a Discover tab, Browse menu, required login, refresh button or a minimum amount of listening history.

A short answer to "Is there a discover playlist in Metrolist?" is: "Standalone Android has Your daily discover on Home. It's a recommendation section based on liked songs rather than a saved Library playlist." If the user means a named YouTube Music playlist or KMP, ask that specific question instead of repeating "what is your question?"

## Scope and source

Verified in `MetrolistGroup/Metrolist@7c5ba3ad22cb562d337897f82163a2b7fce665f3`, the v13.7.0 tag, and source `876e355181fd3b04e7063b10442fef1b721d53b3`:
- `app/src/main/kotlin/com/metrolist/music/ui/screens/HomeScreen.kt`, `HomeSection.DailyDiscover` and its render condition.
- `app/src/main/kotlin/com/metrolist/music/viewmodels/HomeViewModel.kt`, `getDailyDiscover`.
- `app/src/main/res/values/metrolist_strings.xml`, `your_daily_discover`.

These steps are for standalone Android. This reference does not establish a KMP menu path or guarantee that a specific YouTube Music playlist is available to an account.
