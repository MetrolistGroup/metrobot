# metrobot

Metrolist's Discord and Telegram bot for moderation, logging, dehoisting and community support. Some server IDs and policies are hardcoded; review them before running it elsewhere.

## Main features

- [Markdown notes](docs/notes.md), including randomized content, interactive FAQs and Components V2 layouts.
- [AI chat and support](docs/support-routing.md), using staff notes, scoped guides and public project sources. Guidance covers the public Metrolist v14 app on Android, iOS, Windows, macOS and Linux.
- Device specifications and comparisons from GSMArena, NanoReview and Technical City.
- Avatar GIF effects and private OCR/English translation. GIF rendering is local; OCR sends the selected text/images to the configured OpenRouter vision provider, without chat history or shared memory. OCR results do not enter conversation memory and can contain recognition errors.

Use `/help` for commands. `/actions` reads uploaded Android, Linux x64 and Windows x64 assets from the public `nightly` prerelease, with a backup and instability warning. The legacy `actions_workflow_file` and `actions_artifact_names` config keys remain accepted; startup logs that it ignores them.

Discord `>>` commands also work inside `{{...}}` blocks in a message. For example, `im actually pretty tall {{>>wa 191cm to feet and inches}} so i am defo not 3ft tall` runs `>>wa 191cm to feet and inches` and replies to the original message. Multiple blocks run in message order.

## Run

Use the Go version in `go.mod`. Copy `config.example.json` to `config.json`, fill in the required settings, then run:

```sh
go build -o metrobot .
./metrobot
```

The working directory holds `config.json` and `bot.db`. The bot has no web interface or inbound HTTP listener. Keep credentials and runtime data out of Git.

## Upgrade safely

Back up the runtime directory before replacing the binary. Preserve `bot.db`, `config.json` and the live shared-memory file; do not replace them with repository examples or development data. Note migrations run once, so retain both authored markup and migration metadata. The repository's `garmin-memory.md` seeds new installations, not upgrades. The `metrolist_v14_notes_v1` startup migration updates only known repository note versions, preserves staff edits and logs notes requiring manual review. See [note migration details](docs/notes.md#metrolist-v14-note-migration).
