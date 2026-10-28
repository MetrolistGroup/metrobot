# metrobot

Metrolist's Discord and Telegram bot for moderation, logging, dehoisting and community support. Some server IDs and policies are hardcoded; review them before running it elsewhere.

## Main features

- [Markdown notes](docs/notes.md), including randomized content, interactive FAQs and Components V2 layouts.
- [AI chat and support](docs/support-routing.md), using staff notes, scoped guides and public project sources. Unhandled standalone playback requests fall back to approved playback/nightly guidance.
- Device specifications and comparisons from GSMArena, NanoReview and Technical City.
- Avatar GIF effects and private OCR/English translation. GIF rendering is local; OCR sends the selected text/images to the configured OpenRouter vision provider, without chat history or shared memory. OCR results do not enter conversation memory and can contain recognition errors.

Use `/help` for commands.

## Run

Use the Go version in `go.mod`. Copy `config.example.json` to `config.json`, fill in the required settings, then run:

```sh
go build -o metrobot .
./metrobot
```

The working directory holds `config.json` and `bot.db`. The bot has no web interface or inbound HTTP listener. Keep credentials and runtime data out of Git.

## Upgrade safely

Back up the runtime directory before replacing the binary. Preserve `bot.db`, `config.json` and the live shared-memory file; do not replace them with repository examples or development data. Note migrations run once, so retain both authored markup and migration metadata. The repository's `garmin-memory.md` seeds new installations, not upgrades.
