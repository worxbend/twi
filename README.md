<p align="center">
  <img src="docs/assets/twi-banner.svg" alt="twi banner" width="900">
</p>

<p align="center">
  <a href="https://go.dev/"><img alt="Go 1.26" src="https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white"></a>
  <a href="https://www.twitch.tv/"><img alt="Twitch chat" src="https://img.shields.io/badge/Twitch-chat-9146FF?style=for-the-badge&logo=twitch&logoColor=white"></a>
  <a href="Dockerfile"><img alt="Dockerfile" src="https://img.shields.io/badge/Dockerfile-present-2496ED?style=for-the-badge&logo=docker&logoColor=white"></a>
  <a href="docs/config.md"><img alt="Secrets redacted" src="https://img.shields.io/badge/secrets-redacted-111827?style=for-the-badge"></a>
  <a href="LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-10B981?style=for-the-badge"></a>
  <a href="docs/index.md"><img alt="Documentation" src="https://img.shields.io/badge/docs-complete-F59E0B?style=for-the-badge"></a>
</p>

# twi

`twi` is a terminal Twitch chat client with taste. It is keyboard-first, fast to launch, friendly to low-drama terminals, and allergic to leaking your OAuth token.

The project is currently an MVP-shaped Go app for Unix-like terminals and Docker: mock chat and diagnostics are ready without needing Twitch credentials; live Twitch IRC read/send, redacted debug logging, multi-channel UX, focus-aware Twitch event notifications, text-based avatar/badge/emote/emoji rendering, OAuth login, setup, and Unix restrictive credential-file persistence are shipped for their documented paths. Release binary/container packaging is available through the dry-run workflow. Credentialed Twitch release validation remains environment-dependent. Current manual terminal evidence is recorded in [docs/manual-validation.md](docs/manual-validation.md).

```text
        +---------------------------------------------+
        | twi chat --mock                             |
        |                                             |
        |  streamer chat in a terminal, but cute      |
        |  modbot   replies, /me, resize, scroll      |
        |  you      no browser tab circus required    |
        +---------------------------------------------+
```

## Why It Exists

Most chat workflows force you back into a browser tab when the rest of your work is already in a terminal. `twi` keeps chat close to your shell, preserves keyboard-first workflows, and treats credentials like production secrets instead of casual config strings.

What makes this project different:

- A real TUI shell with mock mode, multi-channel state, command palette, selected-message inspect, reply context, local filters, and resize-aware layouts.
- Live Twitch IRC read/send plumbing behind internal interfaces, with startup token validation and redacted auth-refresh behavior.
- Text-first rendering for avatars, badges, emotes, emoji, replies, mentions, moderation notices, and system events: initials chips, stable per-username colors, compact badge labels, matched emote tokens, and native Unicode emoji glyphs, with no inline-image rendering path.
- Async asset plumbing for Twitch avatar/emote/badge metadata without blocking the UI.
- A security posture that keeps OAuth tokens, refresh tokens, client secrets, callback values, and private config out of normal output and debug logs.

## Documentation

The docs are split by audience:

| Need | Read |
| --- | --- |
| Run it quickly | [Quickstart](docs/quickstart.md) |
| Understand every doc | [Documentation Index](docs/index.md) |
| Configure auth and secrets | [Authentication](docs/auth.md) and [Configuration](docs/config.md) |
| Fix setup problems | [Troubleshooting](docs/troubleshooting.md) |
| Run with Docker | [Docker Guide](docs/docker.md) |
| Contribute safely | [Contributing](CONTRIBUTING.md), [Development](docs/development.md), and [Code Style](docs/code-style.md) |
| Report sensitive issues | [Security Policy](SECURITY.md) |
| Understand package boundaries | [Architecture](docs/architecture.md) |
| Cut release artifacts | [Release Packaging](docs/release.md) |

## Start Here

Run the no-risk mock mode:

```sh
go run ./cmd/twi chat --mock --channel demo
```

Build and run the binary:

```sh
go build -o bin/twi ./cmd/twi
./bin/twi chat --mock --channel demo
```

Use Docker:

```sh
docker build -t twi:local .
docker run --rm -it twi:local chat --mock --channel demo
```

Check your setup:

```sh
go run ./cmd/twi doctor
docker run --rm twi:local doctor
```

Run the credential-free release packaging dry-run:

```sh
scripts/release-dry-run.sh --out /tmp/twi-release --image twi:local
```

The dry-run builds trimmed binaries for the supported target matrix, writes
SHA-256 checksums, builds the Docker image, and smokes help, doctor, and mock
chat with local config and Twitch credentials isolated. More detail:
[Release Packaging](docs/release.md).

Install a dry-run binary only after verifying its checksum:

```sh
cd /tmp/twi-release
sha256sum -c twi_linux_amd64.sha256
install -m 0755 twi_linux_amd64 "$HOME/.local/bin/twi"
twi --help
```

On Linux, published releases can also be installed with a curl-pipe script
that downloads the matching binary, verifies its checksum, and wires
`~/.local/bin` into `PATH`:

```sh
curl --proto '=https' --tlsv1.2 -sSf \
  https://github.com/worxbend/twi/releases/latest/download/install.sh | sh
```

More detail, including how to pin a version: [Release Packaging](docs/release.md).

Pick the artifact that matches your OS and CPU. There are no package-manager
manifests, signing, notarization, or registry publishing steps in this release
candidate path yet.

## Live Twitch Chat

Live mode needs a Twitch login, an IRC OAuth token, and at least one channel. Repeat `--channel` to join multiple Twitch IRC channels. The token needs `chat:read`; sending from the composer also needs `chat:edit`. Before starting live IRC, `twi chat` validates token identity, expiry, username match, and required scopes when Twitch OAuth validation is reachable. Definitive invalid-token states stop startup with redacted guidance; transient validation failures warn and continue to IRC authentication. Username/token credentials can come from environment variables, the flat config file, or the private credential store created by `twi login` on supported Unix platforms. Unix builds use a restrictive credential file. Non-Unix builds keep saved credentials disabled; use environment variables or a private flat config file there. Environment and flat config values take precedence over saved credentials. CLI flags currently override channels and config path, not username or token values.

The setup command writes non-secret config values and can hand off to login:

```sh
go run ./cmd/twi setup
go run ./cmd/twi setup --non-interactive --username your_twitch_login --channel somechannel
```

Setup updates username, Twitch app client ID, default channels, avatar mode,
mouse mode, and animation mode. It does not ask for or write OAuth tokens,
refresh tokens, callback codes, OAuth state, authorization URLs, or client
secrets.

```sh
export TWI_TWITCH_USERNAME="your_twitch_login"
export TWI_TWITCH_OAUTH_TOKEN="<oauth token from Twitch>"

go run ./cmd/twi chat --channel somechannel
go run ./cmd/twi chat --channel onechannel --channel anotherchannel
```

The shorter dotenv-style aliases also work:

```sh
export TWITCH_USERNAME="your_twitch_login"
export TWITCH_ACCESS_TOKEN="<oauth token from Twitch>"
export TWITCH_CLIENT_ID="your_client_id"
export TWITCH_CLIENT_SECRET="<client secret from Twitch>"
export TWITCH_REFRESH_TOKEN="<refresh token from Twitch>"
```

If Twitch IRC rejects the access token during login, `twi` will try one OAuth refresh and reconnect when `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`, and `TWITCH_REFRESH_TOKEN` are also configured. On supported Unix platforms, refreshed access and refresh tokens are saved through the private credential store. If saving is unsupported or fails, `twi` keeps the refreshed tokens in memory for the current chat session and reports a redacted warning.

### OAuth Login Command

`twi login` starts a Twitch authorization-code login for:

- `chat:read`
- `chat:edit`
- `channel:manage:broadcast` (view/edit title, category, language, and tags on the Stream Info tab; create/list stream markers on the Misc tab)
- `moderator:read:followers` (follower count in the status line)
- `channel:read:subscriptions` (subscriber count in the status line)
- `clips:edit` (create clips of your active stream with the `/clip` chat command)

The command needs a Twitch app client ID and client secret from environment variables or the flat config file:

```sh
export TWI_TWITCH_CLIENT_ID="your_twitch_client_id"
export TWI_TWITCH_CLIENT_SECRET="<client secret from Twitch>"

go run ./cmd/twi login
```

By default it opens a browser and listens on `http://localhost:1337/api/connect/twitch/callback`; register that redirect URI on the Twitch app or pass `--redirect-uri` for another localhost HTTP callback. For example, if the Twitch app is registered with `http://127.0.0.1:17643/oauth/twitch/callback`, run:

```sh
go run ./cmd/twi login --redirect-uri http://127.0.0.1:17643/oauth/twitch/callback
```

To avoid passing `--redirect-uri` every time, set `twitch_redirect_url` in
`config.toml` or `TWI_TWITCH_REDIRECT_URL`/`TWITCH_REDIRECT_URL` instead — an
explicit `--redirect-uri` flag still takes precedence over all of them.

On supported Unix platforms, success validates the returned token, saves it through the private credential store, and prints only identity/scope/storage status. Non-Unix builds stop before opening the browser and direct users to environment variables or a private flat config file. The command does not print access tokens, refresh tokens, callback codes, OAuth state, authorization URLs, or client secrets.

Use the bounded noninteractive smoke path when you only want to check command wiring:

```sh
go run ./cmd/twi login --dry-run
```

First time here? `go run ./cmd/twi login --write-default-config` writes a
starter `config.toml` (non-secret keys only, current defaults plus anything
already in your environment) at the effective config path before continuing —
but only if that file doesn't already exist yet, so it never clobbers one you
already have.

The file fallback is Unix-only. It stores a separate private `credentials.json` under a `0700` platform config directory, creates the file with `0600` permissions, rejects symlinked credential paths, and opens existing files with no-follow protection. Existing credential files or directories with different modes are rejected instead of reused. Non-Unix builds keep saved credentials disabled. If you keep duplicate credentials in environment variables or `config.toml`, those sources still win until you remove them.

Docker version:

```sh
docker run --rm -it \
  -e TWITCH_USERNAME \
  -e TWITCH_ACCESS_TOKEN \
  twi:local chat --channel somechannel
```

Do not paste real tokens into commits, screenshots, issue comments, terminal recordings, or public support threads. `twi config show`, `twi doctor`, and opt-in debug logs redact secrets by design, but review debug files before sharing because they can still include non-secret IDs, channel names, usernames, and hostnames.

## What Works Today

| Area | Status | Current behavior |
| --- | --- | --- |
| Mock chat | Ready | `twi chat --mock [--channel demo]` runs without Twitch credentials or network access. |
| Multi-channel live IRC read/send | Partial | `twi chat --channel <channel> [--channel other]` validates startup credentials when Twitch OAuth validation is reachable, then can read, send, reply, and send `/me` actions for configured channels when env/config credentials or saved credentials on supported Unix platforms are present; broader live manual evidence remains future work. |
| Config commands | Ready | `twi config show` prints effective flat config with secrets redacted; `twi config path` shows the default config path. |
| Diagnostics | Ready | `twi doctor` checks config path, credential presence, Twitch OAuth identity/expiry/scope validation, refresh availability, username mismatch, Twitch IRC reachability, terminal hints, cache writability/pruning, and feature modes. Live chat also preflights token validation before IRC startup when validation is reachable. |
| Debug logging | Ready | Redacted JSON debug logs can be enabled with `debug_logging = true`, `TWI_DEBUG_LOG=true`, or `--debug-log` on chat, login, and doctor. Logs use curated fields for auth, transport, send, asset, and render diagnostics instead of raw struct or raw tag dumps. |
| Avatar metadata | Partial | When live chat runs with `avatar_mode = "initials"` (the default) plus Twitch API credentials and a writable cache, visible author display names are batched through Helix Get Users to render a per-author `[XY]` initials chip; `avatar_mode = "off"` hides the chip. There is no image rendering path. |
| Emote/badge metadata | Partial | Live startup can wire Twitch IRC fragment data for emotes and Helix-backed fallback emote/badge metadata behind config, cache, credential, and terminal gates, always rendering compact badge labels (for example `[mod]`, `[sub]`, `[vip]`) and matched emote text tokens (for example `Kappa`). |
| Login/setup | Partial | `twi setup` creates or updates non-secret flat config values and can hand off to `twi login`; on supported Unix builds, `twi login` saves through the restrictive credential-file fallback. Non-Unix builds keep env/config credentials as the supported path. |
| Multi-channel UX | Partial | Messages, unread counts, scroll, drafts, replies, sends, and local view filters are per-channel. Normal and wide terminals show a keyboard-first channel sidebar with connection indicators, unread counts, and filter markers; `ctrl+p` opens a keyboard command palette for common actions, panel toggles, channel switching, local filters, local clear, and live reconnect restart. Optional mouse support can scroll chat, click channels, focus the composer, and select messages. Twitch `USERNOTICE` events such as raids carry normalized event IDs; when the relevant chat is not active, the terminal is blurred, or another panel has focus, `twi` attempts a desktop system notification, falls back to a terminal bell, and shows a status-line notification summary. Selected messages can be inspected in a redacted diagnostics panel even when filters hide them from the chat view. Narrow terminals collapse channel state into the status line. Twitch IRC connect/reconnect/disconnect callbacks are connection-level and are shown on configured channel states rather than as independent per-channel transport events. Manual reconnect tears down the active live IRC transport before creating a fresh one while preserving per-channel UI state. |
| Text-only asset rendering | Ready | Avatars, badges, emotes, and emoji always render as text: `[XY]` initials chips (or nothing when `avatar_mode = "off"`), compact badge labels, matched emote text tokens, and native Unicode emoji glyphs. There is no image decode, cache, or terminal-graphics rendering path; missing metadata, missing credentials, or lookup failures simply keep the text fallback. |
| Theming | Ready | 13 built-in presets (Claude, Codex, Btop, Nord, Dracula, Gruvbox, Solarized Dark, Monokai, One Dark, Tokyo Night, Catppuccin Mocha, Rose Pine, Mono) plus a custom hex palette apply across every widget. The application canvas is derived slightly darker than the selected background, while icon-titled panes use the raised surface color, quiet frames, independently colored left rails, and shared-clock focus gradients on auxiliary panes. The Chat pane frame and title remain static. Consecutive messages from the same author share one surface/rail group; a subtle horizontal rule and alternating group surface appear only when the visible author changes. Each username also keeps a stable hash-derived readable color. The borderless composer uses a raised surface, focus rail, block cursor, and `Chat · #channel · state` footer inspired by modern coding TUIs. `ctrl+t` opens a btop-style settings view that live-previews a theme as you move the selection and persists it with `enter` (`esc` reverts); `twi profile list\|show\|set` manages the same setting from the CLI. |
| Animation | Ready | A shared ~10fps clock (disabled when `animation_mode = "off"`) drives seamless mirrored gradients, pulsing LIVE/REC and incoming-message rails, typewriter chat and command-palette reveals, and a staged ~2s block-logo startup sequence (skippable by any keypress). Moving gradients travel through a `start → end → start` palette so either side wraps without a visible color seam. The Chat pane border/title are deliberately excluded. Reduced mode slows decorative gradient motion. |
| Live status telemetry | Partial | The status bar shows real Twitch broadcast status via Helix "Get Streams" polling (LIVE + elapsed on-air time + viewer count) when `stream_status_mode` and Twitch API credentials allow it, otherwise OFFLINE; follower and subscriber counts poll Helix "Get Channel Followers"/"Get Broadcaster Subscriptions" every 2 minutes when credentials and the `moderator:read:followers`/`channel:read:subscriptions` scopes allow it; REC reflects `debug_logging`; CPU%/memory/FPS are twi's own process stats; "chat" bitrate is derived chat-message throughput, not stream encode bitrate. `--mock` simulates a fixed demo LIVE state. |
| Emote autocomplete | Partial | `ctrl+e` opens a searchable emote/emoji picker and a persistent quick-select row (third `tab` stop). A built-in emoji set is always available; real Twitch global/channel emotes merge into it when `emote_autocomplete_mode` and credentials allow. Mock mode demonstrates the combined workflow and emoji-rich messages without network access. |
| Activity log column | Partial | On wide terminals (140+ columns), the Chat tab shows a right-hand Activity column alongside chat (and the optional channel sidebar) covering every alert twi can currently detect over IRC and Helix polling: raids, subs/resubs/gift subs/gift upgrades, announcements, charity donations, and moderation actions from Twitch IRC events; cheers (detected from a chat message's "bits" tag, since Twitch sends cheers as ordinary PRIVMSGs, not a USERNOTICE); new followers (detected by polling Get Channel Followers and diffing against previously seen followers, since Twitch only pushes follow events through EventSub, not IRC or any webhook twi can receive); stream went-live/went-offline transitions (detected by polling Get Streams, the same status the LIVE/OFFLINE badge uses); and clips created with `/clip`. Alerts that only exist through EventSub (hype train, polls/predictions, channel-point redemption details) aren't available since twi has no EventSub/WebSocket connection. Hidden below 100 columns and on the Stream Info/Misc tabs. |
| `/clip` command | Partial | Typing `/clip`, `/clip T-5m`, or `/clip T-4m T-2m` in the composer creates a clip of the current stream through Helix "Create Clip" when credentials, the `clips:edit` scope, and being live all allow it; the API has no start/end/duration parameter, so the `T-` offsets are only echoed back next to the clip's edit URL as a trim reminder, never sent to Twitch. |

Manual validation evidence for the current environment is tracked in
[docs/manual-validation.md](docs/manual-validation.md). Credentialed Twitch chat
is only claimed when that document records a complete credential set.

## Controls

| Key | Action |
| --- | --- |
| `alt+1` / `alt+2` / `alt+3` | Switch the top tab bar between Chat, Stream Info, and Misc. |
| `ctrl+p` | Open or close the command palette. |
| `ctrl+t` | Open or close theme settings; live-preview a theme with `up`/`down`, `enter` to save, `esc` to revert. |
| `ctrl+e` | Open or close the searchable emote picker; filter by typing, `up`/`down` to select, `enter` to insert. |
| `tab` | Cycle focus between chat, composer, and the emotes quick-select row. |
| `left` / `right` | Move the emotes quick-select row's highlighted emote (when it has focus). |
| `?` | Toggle expanded help. |
| `pgup` / `pgdown` | Scroll chat. |
| `up` / `down` | Select messages for reply or inspect mode (chat focus), or navigate an open overlay. |
| `1` / `2` / `3` / `4` | Toggle local filters for mentions, broadcaster/mod/VIP messages, notices, and errors from chat focus. |
| `0` | Reset active-channel message filters. |
| `r` | Reply to the selected message. |
| `i` | Open or close the selected-message inspect panel. |
| `ctrl+l` | Clear the active channel's local chat history. |
| `ctrl+r` | Restart the active live chat source when supported, preserving channel history and drafts. |
| `esc` | Close inspect mode, cancel reply mode, or close an open overlay. |
| `enter` | Send from the composer in live mode, or insert the selected emote when the emotes row/picker has focus. |
| `/me does a thing` | Send a Twitch action message. |
| `/clip`, `/clip T-5m`, `/clip T-4m T-2m` | Create a clip of the current stream (see [Clip Command](#clip-command)). |

Mouse support is enabled by default. Set `enable_mouse = false` or `TWI_ENABLE_MOUSE=false` to keep terminal mouse reporting disabled; all workflows remain available from the keyboard.

Terminal focus reporting is enabled for interactive chat sessions. Terminals that
do not report focus still behave as focused, so system-event notifications avoid
extra alerts unless another in-app panel or channel has the user's attention.
Desktop notifications are best effort and dependency-free: Linux uses
`notify-send`, macOS uses `osascript`, Windows uses PowerShell toast APIs, and
unsupported or unavailable notification commands fall back to a terminal bell.

## Stream Info Tab

`twi` opens with a top tab bar that shows the configured Twitch username and
active chat name; `alt+1` selects Chat (the default), `alt+2` selects Stream
Info, and `alt+3` selects Misc. Stream Info is a dedicated screen for viewing
and editing your own channel's title, category, language, and tags without
leaving the terminal. On that tab, `up`/`down` selects a
field and `ctrl+s` saves every changed field to Twitch. `enter` on
Title/Language/Tags edits free text (`enter` again commits, `esc` cancels).
`enter` on Category instead opens a live search against Twitch's category
list (Helix Search Categories): type to filter, `up`/`down` to pick a
result, `enter` to select it, or `esc` to cancel - category is always a
real Twitch category chosen from the API, never free-typed text.

The Stream Info tab needs a Twitch app client ID and OAuth token (the same
credentials live chat uses) and the `channel:manage:broadcast` scope, which
`twi login` requests automatically alongside `chat:read`/`chat:edit`. Without
those credentials the tab explains what is missing instead of failing
silently.

## Misc Tab

`alt+3` opens Misc, which lists your own stream's markers (Twitch Helix
Stream Markers) and lets you add new ones. `up`/`down` selects a marker,
`enter` opens a free-text description prompt (`enter` again saves the
marker at the current live position, `esc` cancels the prompt without
creating one). Creating a marker only succeeds while you're live - Twitch
rejects it otherwise, and that error shows in the tab. Misc uses the same
`channel:manage:broadcast` scope and credentials as Stream Info, so no
separate login step is needed.

## Clip Command

Typing `/clip` in the composer and pressing `enter` creates a Twitch clip of
your current stream (Twitch Helix "Create Clip"), needing the `clips:edit`
scope that `twi login` requests automatically. `/clip` only succeeds while
you're live; that error, and a missing-scope error, both show as
human-readable status-line text instead of raw JSON.

Twitch's Create Clip API has no parameter for a start time, end time, or
duration - it always captures approximately the last 30-60 seconds at the
moment of the call, with no way to reach further back. `/clip T-5m` and
`/clip T-4m T-2m` accept those offsets anyway, but only to echo the range
you asked for back to you alongside the clip's edit URL, as a reminder of
what to trim to in Twitch's own clip editor - the offsets are never sent to
Twitch, since the API can't act on them. A successful clip also appears in
the Activity log column with its edit URL.

## Configure It

Use environment variables for quick runs:

```sh
export TWI_DEFAULT_CHANNELS="somechannel"
export TWI_ANIMATION_MODE="fast"
export TWI_ENABLE_MOUSE="true"
export TWI_AVATAR_MODE="initials"
export TWI_THEME_NAME="claude"
export TWI_STREAM_STATUS_MODE="auto"
export TWI_EMOTE_AUTOCOMPLETE_MODE="auto"
export TWI_DEBUG_LOG="false"
```

Or create the flat config file shown by:

```sh
twi config path
```

For a guided path, run `twi setup`. For automation or CI, use
`twi setup --non-interactive` with flags such as `--username`, `--channel`,
`--avatar-mode`, and `--animation-mode`.

Example:

```toml
twitch_username = "your_twitch_login"
twitch_oauth_token = ""
twitch_refresh_token = ""
twitch_client_id = ""
twitch_client_secret = ""
twitch_redirect_url = ""
default_channels = "somechannel"
enable_mouse = true
avatar_mode = "initials"
animation_mode = "fast"
theme_name = "claude"
theme_background = ""
theme_foreground = ""
theme_accent = ""
theme_muted = ""
theme_border = ""
theme_surface = ""
theme_warning = ""
theme_error = ""
theme_success = ""
stream_status_mode = "auto"
emote_autocomplete_mode = "auto"
debug_logging = false
debug_log_path = ""
```

### Themes

`theme_name` selects one of 13 built-in presets — `claude` (default), `codex`,
`btop`, `nord`, `dracula`, `gruvbox`, `solarized-dark`, `monokai`, `one-dark`,
`tokyo-night`, `catppuccin-mocha`, `rose-pine`, `mono` — applied across every
widget, including a slightly darker derived application canvas behind raised
pane surfaces. Set `theme_name = "custom"`
and fill in the `theme_*` hex fields above for your own palette; unset custom
fields fall back to no styling for that role.

The background isn't just painted within the rendered viewport: interactive
sessions send an OSC 11 escape sequence that overrides the terminal
emulator's own default background color to match that derived canvas, and restore the
terminal's original background (OSC 111) when `twi` exits. This is supported
by xterm, iTerm2, kitty, Alacritty, WezTerm, and VTE-based terminals;
unsupported terminals simply ignore the sequence.

Manage themes from the CLI:

```sh
twi profile list                 # preset names, with the active one marked
twi profile show                 # active theme name + resolved hex values
twi profile set nord             # persist a built-in preset
twi profile set custom --background '#000000' --foreground '#ffffff' --accent '#ff00ff'
```

Or interactively: press `ctrl+t` in the chat shell for a btop-style settings
view that live-previews each theme as you move the selection, `enter` to save,
`esc` to revert.

The status bar's LIVE indicator reflects the channel's real Twitch broadcast
status (polled via Helix "Get Streams" every 60s when `stream_status_mode`
and Twitch API credentials allow it) — not just the local IRC connection.
REC reflects `debug_logging` (twi's own debug-log recording), since twi has
no other "recording" concept. The "chat" throughput figure is derived from
actual incoming chat-message bytes, not stream encode bitrate, which Twitch
does not expose through any public API.

For support diagnostics, enable redacted JSON logs explicitly:

```sh
twi chat --channel somechannel --debug-log
twi login --debug-log --debug-log-path /tmp/twi-debug.log
twi doctor --debug-log
```

When no path is provided, the log file is `debug.log` under the platform cache
directory. Existing debug-log files that are directories, symlinks, or allow
group/other access are rejected; Unix builds also open the final log path with
no-follow semantics before validating the opened file.

Nested TOML tables are not implemented yet. Keep the file flat.

Prefer `twi login` for saved tokens on supported Unix platforms. Leave secret
values empty in shared examples and docs. If you also keep real tokens in the
flat config, keep that file private to your user account, for example with
`chmod 600`; flat config values still take precedence over saved credentials.

## Known Release Gaps

The release candidate path is intentionally explicit about what is not yet
claimed:

- Credentialed Twitch read/send/reconnect and browser login still require a
  real Twitch app, username, token set, and manual validation evidence.
- Exact Docker CLI smokes need a Docker-enabled host; Podman-equivalent smokes
  do not replace the final Docker check.
- Refreshed IRC tokens are persisted only when the supported credential store
  is available; otherwise they remain in memory for the current process with a
  redacted warning.
- Non-Unix saved credentials are out of scope; use environment variables or a
  private flat config file if you build for an unsupported platform.
- Package-manager manifests, signing, notarization, registry publishing, and
  SBOM/provenance are post-release work.

## Docker And Deploy

This is a terminal app, so "deploy" usually means "ship the binary or container to the machine where a human will run it in a real TTY."

```sh
docker build -t twi:local .
cp .env.example .env
docker compose run --rm mock
docker compose run --rm doctor
docker compose run --rm live
```

For live Docker runs, put real values only in your local ignored `.env`, pass credentials through environment variables, or use a private runtime secret mechanism. Do not bake tokens into the image.

More detail: [Docker Guide](docs/docker.md).

`twi` is also packaged as a snap (`snap/snapcraft.yaml`, classic confinement,
`amd64`/`arm64`). Once published:

```sh
sudo snap install twi --classic
```

Release binary, container, and snap packaging are covered by
[Release Packaging](docs/release.md). The release dry-run and snap build are
separate manual/tag workflows, not part of the default pull-request gate.

## Developer Commands

The default CI quality gate runs this same credential-free command set from a
clean checkout:

```sh
export GOTOOLCHAIN=auto TERM=xterm-256color
export XDG_CONFIG_HOME="$(mktemp -d)" XDG_CACHE_HOME="$(mktemp -d)"
export TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN=
export TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET=
export TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN=
export TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET=
go version
go mod tidy
go fmt ./...
git diff --exit-code
go vet ./...
go test ./...
go test -race ./...
go tool govulncheck ./...
go tool staticcheck ./...
go build -o /tmp/twi-validation ./cmd/twi
go run ./cmd/twi --help
go run ./cmd/twi chat --mock --channel example
go run ./cmd/twi chat --mock --channel one --channel two
go run ./cmd/twi doctor
go run ./cmd/twi config show
git diff --check origin/main...HEAD
```

Credentialed Twitch chat and Docker-only checks are manual or release-specific
checks, not part of the default pull request gate.

If your PR targets a different base branch, replace `origin/main` with that
branch. Use plain `git diff --check` for uncommitted local changes.

Restricted environment cache-friendly form:

```sh
GOTOOLCHAIN=local GOCACHE=/tmp/twi-gocache GOMODCACHE=/tmp/twi-gomodcache go test ./...
```

## Contributor Map

- [Contributing](CONTRIBUTING.md) explains the support boundary, safe workflow, verification commands, PR checklist, and secret-handling rules.
- [Code Style](docs/code-style.md) defines package ownership, rendering rules, debug logging rules, comments, tests, and documentation style.
- [Architecture](docs/architecture.md) shows how config, Twitch transport, Bubble Tea state, rendering, assets, storage, and debug logging fit together.
- [Development](docs/development.md) records the deeper implementation state, toolchain, quality gates, and testing strategy.

## Project Direction

Near-term work is focused on release evidence and credentialed Twitch validation when credentials are available. The product source of truth lives in [docs/index.md](docs/index.md) and the product docs under `docs/product/`.

## License

`twi` is released under the [MIT License](LICENSE).
