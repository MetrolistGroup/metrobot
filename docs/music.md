# Discord YouTube voice playback

Music uses native disgo v0.19.6 voice transport and **DAVE encryption**, via
`github.com/disgoorg/godave/golibdave v0.1.0`. No DiscordGo bridge, noop DAVE
session, shell expansion, queue database, or separate music service is used.
The existing message-link YouTube cleaner is unchanged.

## Commands

All three commands are registered only in the configured Discord guild.
Responses are ephemeral. Any guild member can inspect the queue; mutations
require the caller to be in the bot's **same normal voice channel**. Stage
channels are unsupported. The bot needs Connect and Speak to join.

| Command | Effect |
| --- | --- |
| `/play query:URL-or-search` | Add one video. First search result wins. |
| `/play` | Resume paused playback, without adding another track. |
| `/pause` | Pause audio and apply backpressure to ffmpeg. |
| `/queue` | Show current track, state, last playback error and pending page 1. |
| `/queue page:2` | Show another pending page (5 entries each). |
| `/queue action:skip` | Cancel the current lookup/decoder and advance; no repeat of the skipped track. |
| `/queue action:stop` | Clear everything, cancel tools and disconnect. |
| `/queue action:remove position:3` | Remove pending entry 3, using the absolute position displayed by `/queue`. |
| `/queue action:clear` | Clear pending entries, keeping the current track. |
| `/queue action:shuffle` | Shuffle pending entries, keeping the current track. |
| `/queue action:loop mode:off` | Disable repeating (the default). |
| `/queue action:loop mode:track` | Repeat the current successful track until skipped. |
| `/queue action:loop mode:queue` | Append each successful track to the end. |

Videos resolve lazily at the front of the queue. Pending entries therefore
show the submitted search/URL until resolved. `/play` acknowledges the enqueue;
use `/queue` to see metadata or failures. Playback advances automatically on
EOF or a track error. Stop/disconnect cleanup runs asynchronously; new playback
is rejected until the old session has finished closing. Bot shutdown waits for
cleanup. There are no redundant top-level skip/stop commands.

Seek and volume are intentionally omitted: they would need a decoder restart
and additional offset/filter state. Looping is included without another
playback pipeline.

## Bounds and lifecycle

- At most 100 current + pending entries, 200 UTF-8 bytes per query, and 3 hours
  per video. Live/upcoming streams and playlist URLs are rejected.
- Only HTTPS YouTube video URLs are accepted. Tracking parameters are removed;
  URL timestamps do not seek. Other input is explicitly `ytsearch1:` text.
- Metadata must identify a finite YouTube video. Media extraction is repeated
  just before playback, and ffmpeg receives only an HTTPS `*.googlevideo.com`
  URL. Generic URLs, internal addresses, external extractor configuration,
  playlist crawling and user-supplied tool flags are unavailable.
- Lookups have a 30-second deadline, bounded stdout (256 KiB metadata, 8 KiB
  media URL), limited retries and socket timeouts. Each track has a 3h15m wall
  deadline; ffmpeg network reads time out after 15 seconds. Tool stderr is not
  exposed to users, so signed URLs cannot leak into Discord errors.
- Processes are launched directly with argument arrays. Cancellation kills
  the Linux process group (including yt-dlp's JS helpers) and waits for the
  launched process. Every ffmpeg error/exit path closes its pipe and waits.
- No pending/current track for 60 seconds disconnects the bot. Pausing for
  10 minutes disconnects it. Losing the last human listener, being kicked,
  moved to another channel, or server-muted also cancels the session.
- Queues and repeat state live in memory: **restart, disconnect and stop lose
  the queue**. There is no persistence or recovery.

ffmpeg encodes libopus, 48 kHz stereo, 20 ms frames at 96 kbps. A small local
Ogg parser checks stream identity, sequencing, continuation, packet bounds,
headers and Opus duration. It consumes only our ffmpeg stdout, not arbitrary
user Ogg streams, so it does not calculate Ogg CRCs. Tests include real ffmpeg
sine audio and malformed/continued synthetic pages. The worker paces packets
and uses disgo's DAVE-enabled UDP transport directly; it avoids the default
AudioSender's provider/cancellation lifecycle. Pause/end sends Discord's five
silent frames and clears speaking state.

## Production build: native DAVE is mandatory

The music feature is opt-in at build time, not a runtime encryption fallback:

```sh
export PKG_CONFIG_PATH="$HOME/.local/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
export LD_LIBRARY_PATH="$HOME/.local/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
pkg-config --modversion dave
CGO_ENABLED=1 go build -tags music -o metrobot .
```

Required native version: **discord/libdave v1.1.0**, whose Git tag is
`v1.1.0/cpp`. This is the version named by the v0.1.0 Go binding's
[`release.txt`](https://github.com/disgoorg/godave/blob/v0.1.0/libdave/release.txt).
Both `godave` and `golibdave` are pinned to v0.1.0.

The CGO binding runs `pkg-config --cflags --libs dave` and includes `dave.h`.
Install the matching platform/architecture files:

- `dave.h`, directly in an include directory (upstream archives put it under
  `include/dave/dave.h`).
- `libdave.so`, in a runtime-visible library directory.
- `dave.pc`, in a pkg-config directory, with `Cflags: -I<include-dir>` and
  `Libs: -L<lib-dir> -ldave`. The upstream installer adds a runtime rpath too.
- A C compiler and pkg-config at build time; the shared library and everything
  listed by `ldd libdave.so` at runtime.

Upstream provides an auditable, pinned
[installation script](https://github.com/disgoorg/godave/blob/v0.1.0/scripts/libdave_install.sh):

```sh
curl -fSL https://raw.githubusercontent.com/disgoorg/godave/v0.1.0/scripts/libdave_install.sh -o libdave_install.sh
# Read/audit the downloaded script before running it.
sh ./libdave_install.sh v1.1.0
```

It installs into `$HOME/.local`. For Linux x86-64 it first downloads
`https://github.com/discord/libdave/releases/download/v1.1.0/cpp/libdave-Linux-X64-boringssl.zip`.
It requires curl/unzip for prebuilt files. If unavailable, it builds the
recursive libdave checkout using git, a C/C++ toolchain, make and CMake;
additional vcpkg tools such as Ninja, zip/unzip, Perl and NASM may be needed.
For musl, build libdave for musl instead of loading the glibc prebuilt library.
See the [upstream instructions](https://github.com/disgoorg/godave/tree/v0.1.0).

Without `-tags music`, `CGO_ENABLED=0 go build .` remains supported. The commands
and help still exist but explicitly say that music is unavailable; no voice
connection is attempted. A music-tagged build without CGO/native libdave fails
at build time instead of silently using noop encryption.

## Runtime and containers

Put **yt-dlp, ffmpeg with libopus, and deno** on the bot's PATH. yt-dlp must be
recent enough to support `--js-runtimes deno`; keep it updated when YouTube
changes extraction. HTTPS CA certificates and outbound HTTPS/Discord voice UDP
are required. YouTube may reject datacenter IPs, age-restricted/private videos
or requests requiring cookies; those tracks fail and advance. This feature
does not bypass those restrictions or read cookies from yt-dlp configuration.

The existing static/distroless `Dockerfile` is a **non-music build**. It has
neither the native shared library nor extraction tools. Do not deploy it and
expect voice playback. A music image needs:

1. A CGO-capable Go build stage with matching libdave headers/library/`dave.pc`,
   using `CGO_ENABLED=1 go build -tags music`.
2. A compatible non-static Linux runtime (for example a recent glibc Debian
   image when using glibc libdave), containing libdave and its `ldd` dependencies,
   CA certificates, ffmpeg/libopus, yt-dlp and deno. A Python-installed yt-dlp
   also needs its Python runtime.
3. The dynamic loader path or rpath pointing at the copied libdave directory,
   the existing bot configuration/data mounts, and an unprivileged runtime
   user. No token or runtime database belongs in the build context.

Copying only the bot binary into a static distroless image cannot work.
The host-installed library must be the same architecture and libc as the
container; compile in the container if they differ.

## Verification

```sh
go test ./...
go test -race ./internal/music ./discord
go vet ./...
CGO_ENABLED=0 go build .
# With native DAVE installed:
CGO_ENABLED=1 go test -tags music ./...
CGO_ENABLED=1 go test -race -tags music ./internal/music ./discord
CGO_ENABLED=1 go vet -tags music ./...
CGO_ENABLED=1 go build -tags music .
```

Unit tests use fake voice sinks and fake playback; no real gateway, token or
runtime database is used. Real Discord/DAVE negotiation and YouTube access
must be checked in the intended deployment environment.
