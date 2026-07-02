<p align="center">
  <img src="docs/assets/twi-banner.svg" alt="twi banner" width="900">
</p>

<p align="center">
  <a href="https://go.dev/"><img alt="Go 1.26" src="https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white"></a>
  <a href="https://www.twitch.tv/"><img alt="Twitch chat" src="https://img.shields.io/badge/Twitch-chat-9146FF?style=for-the-badge&logo=twitch&logoColor=white"></a>
  <a href="Dockerfile"><img alt="Docker ready" src="https://img.shields.io/badge/Docker-ready-2496ED?style=for-the-badge&logo=docker&logoColor=white"></a>
  <a href="docs/config.md"><img alt="Secrets redacted" src="https://img.shields.io/badge/secrets-redacted-111827?style=for-the-badge"></a>
</p>

# twi

`twi` is a terminal Twitch chat client with taste. It is keyboard-first, fast to launch, friendly to low-drama terminals, and allergic to leaking your OAuth token.

The project is currently an MVP-shaped Go app: mock chat is ready without the network; live Twitch IRC read/send, diagnostics, multi-channel UX, and inline image plumbing are partially shipped; and login/setup, secure credential storage, default live image resolver wiring, and manual Kitty/Ghostty image validation are still planned.

```text
        +---------------------------------------------+
        | twi chat --mock                             |
        |                                             |
        |  ariadne  chat in a terminal, but cute      |
        |  modbot   replies, /me, resize, scroll      |
        |  you      no browser tab circus required    |
        +---------------------------------------------+
```

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

## Live Twitch Chat

Live mode needs a Twitch login, an IRC OAuth token, and at least one channel. Repeat `--channel` to join multiple Twitch IRC channels. The token needs `chat:read`; sending from the composer also needs `chat:edit`. Username/token credentials currently come from environment variables or the flat config file. CLI flags currently override channels and config path, not username or token values.

```sh
export TWI_TWITCH_USERNAME="your_twitch_login"
export TWI_TWITCH_OAUTH_TOKEN="<your-twitch-oauth-token>"

go run ./cmd/twi chat --channel somechannel
go run ./cmd/twi chat --channel onechannel --channel anotherchannel
```

The shorter dotenv-style aliases also work:

```sh
export TWITCH_USERNAME="your_twitch_login"
export TWITCH_ACCESS_TOKEN="<your-twitch-access-token>"
export TWITCH_CLIENT_ID="your_client_id"
export TWITCH_CLIENT_SECRET="<your-twitch-client-secret>"
export TWITCH_REFRESH_TOKEN="<your-twitch-refresh-token>"
```

If Twitch IRC rejects the access token during login, `twi` will try one in-memory OAuth refresh and reconnect when `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`, and `TWITCH_REFRESH_TOKEN` are also configured. It does not write the refreshed token back to disk yet.

Docker version:

```sh
docker run --rm -it \
  -e TWITCH_USERNAME \
  -e TWITCH_ACCESS_TOKEN \
  twi:local chat --channel somechannel
```

Do not paste real tokens into commits, screenshots, issue comments, terminal recordings, or public support threads. `twi config show` and `twi doctor` redact secrets by design.

## What Works Today

| Area | Status | Current behavior |
| --- | --- | --- |
| Mock chat | Ready | `twi chat --mock [--channel demo]` runs without Twitch credentials or network access. |
| Multi-channel live IRC read/send | Partial | `twi chat --channel <channel> [--channel other]` can read, send, reply, and send `/me` actions for configured channels when env/config credentials are present; broader live manual evidence remains future work. |
| Config commands | Ready | `twi config show` prints effective flat config with secrets redacted; `twi config path` shows the default config path. |
| Diagnostics | Partial | `twi doctor` checks config path, credential presence, Twitch OAuth identity/expiry/scope validation, refresh availability, username mismatch, Twitch IRC reachability, terminal hints, Kitty/Ghostty signals, cache writability/pruning, and feature modes. |
| Avatar metadata | Partial | When live chat runs with `avatar_mode = "image"` plus Twitch API credentials, visible author avatar URLs are batched through Helix Get Users and cached; app asset events can prepare fixed-width avatar cells when an asset resolver/renderer is installed. |
| Emote/badge metadata | Partial | Internal Helix adapters and cache-backed resolvers can turn known Twitch emote and badge IDs into public image-capable refs while keeping Unicode and exact emote-token fallbacks stable; app asset events can refresh visible rows without scroll or composer jumps. |
| Login/setup | Planned | `twi login` is advertised but exits as planned/not implemented. |
| Multi-channel UX | Partial | Messages, unread counts, scroll, drafts, replies, and sends are per-channel. Normal and wide terminals show a keyboard-first channel sidebar with connection indicators and unread counts; `ctrl+p` opens a keyboard command palette for common actions, panel toggles, channel switching, local clear, and reconnect requests. Optional mouse support can scroll chat, click channels, focus the composer, and select messages. Selected messages can be inspected in a redacted diagnostics panel. Narrow terminals collapse channel state into the status line. Twitch IRC connect/reconnect/disconnect callbacks are connection-level and are shown on configured channel states rather than as independent per-channel transport events. |
| Inline terminal images | Partial | The renderer and app event path can substitute prepared fixed-width cells for visible avatar, badge, emote, and emoji rows; default live resolver wiring and manual Kitty/Ghostty validation remain planned. |

## Controls

| Key | Action |
| --- | --- |
| `ctrl+p` | Open or close the command palette. |
| `tab` | Switch focus between chat and composer. |
| `?` | Toggle expanded help. |
| `pgup` / `pgdown` | Scroll chat. |
| `up` / `down` | Select messages for reply or inspect mode. |
| `r` | Reply to the selected message. |
| `i` | Open or close the selected-message inspect panel. |
| `ctrl+l` | Clear the active channel's local chat history. |
| `ctrl+r` | Request a reconnect for the active chat source when the client supports it. |
| `esc` | Close inspect mode or cancel reply mode. |
| `enter` | Send from the composer in live mode. |
| `/me does a thing` | Send a Twitch action message. |

Mouse support is enabled by default. Set `enable_mouse = false` or `TWI_ENABLE_MOUSE=false` to keep terminal mouse reporting disabled; all workflows remain available from the keyboard.

## Configure It

Use environment variables for quick runs:

```sh
export TWI_DEFAULT_CHANNELS="somechannel"
export TWI_ANIMATION_MODE="fast"
export TWI_ENABLE_MOUSE="true"
export TWI_AVATAR_MODE="initials"
export TWI_EMOTE_MODE="text"
```

Or create the flat config file shown by:

```sh
twi config path
```

Example:

```toml
twitch_username = "your_twitch_login"
twitch_oauth_token = "PLACEHOLDER_TWITCH_OAUTH_TOKEN"
twitch_refresh_token = "PLACEHOLDER_TWITCH_REFRESH_TOKEN"
default_channels = "somechannel"
enable_kitty_images = true
enable_mouse = true
image_mode = "auto"
avatar_mode = "initials"
emoji_mode = "unicode"
emote_mode = "text"
animation_mode = "fast"
```

Nested TOML tables are not implemented yet. Keep the file flat.

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

Credentialed Twitch chat, Docker-only checks, and Kitty/Ghostty inline-image
validation are manual or release-specific checks, not part of the default pull
request gate.

If your PR targets a different base branch, replace `origin/main` with that
branch. Use plain `git diff --check` for uncommitted local changes.

Restricted environment cache-friendly form:

```sh
GOTOOLCHAIN=local GOCACHE=/tmp/twi-gocache GOMODCACHE=/tmp/twi-gomodcache go test ./...
```

## Docs For Humans

- [Quickstart](docs/quickstart.md)
- [Docker Guide](docs/docker.md)
- [Authentication](docs/auth.md)
- [Configuration](docs/config.md)
- [Development](docs/development.md)
- [Terminal Images](docs/terminal-images.md)

## Project Direction

Near-term work is focused on keeping the MVP sharp: CI coverage, default live asset/image wiring, login/setup with secure storage, reconnect hardening, filters, redacted debug logging, release packaging, and manual terminal validation. The source of truth lives in the product docs under `docs/`.
