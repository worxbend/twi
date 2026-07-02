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

The project is currently an MVP-shaped Go app: mock chat is ready without the network, one-channel live Twitch IRC read/send is partially shipped for one configured channel, diagnostics are partially shipped, and login, multi-channel UX, and inline terminal images are still planned.

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

Live mode needs a Twitch login, an IRC OAuth token, and one channel. The token needs `chat:read`; sending from the composer also needs `chat:edit`. Username/token credentials currently come from environment variables or the flat config file. CLI flags currently override the channel and config path, not username or token values.

```sh
export TWI_TWITCH_USERNAME="your_twitch_login"
export TWI_TWITCH_OAUTH_TOKEN="<your-twitch-oauth-token>"

go run ./cmd/twi chat --channel somechannel
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
| One-channel live IRC read/send | Partial | `twi chat --channel <channel>` can read, send, reply, and send `/me` actions for one channel when env/config credentials are present; token identity/scope validation and broader live manual evidence remain future work. |
| Config commands | Ready | `twi config show` prints effective flat config with secrets redacted; `twi config path` shows the default config path. |
| Diagnostics | Partial | `twi doctor` checks config path, credential presence, unverified scope hints, Twitch IRC reachability, terminal hints, Kitty/Ghostty signals, cache writability, and feature modes; Helix-backed identity/expiry/scope validation is planned. |
| Login/setup | Planned | `twi login` is advertised but exits as planned/not implemented. |
| Multi-channel live chat | Planned | Current live mode intentionally accepts only one channel. |
| Inline terminal images | Planned | Current rendering uses stable text, initials, Unicode, badge, and emote-token fallbacks only. |

## Controls

| Key | Action |
| --- | --- |
| `tab` | Switch focus between chat and composer. |
| `?` | Toggle expanded help. |
| `pgup` / `pgdown` | Scroll chat. |
| `up` / `down` | Select messages for reply mode. |
| `r` | Reply to the selected message. |
| `esc` | Cancel reply mode. |
| `enter` | Send from the composer in live mode. |
| `/me does a thing` | Send a Twitch action message. |

## Configure It

Use environment variables for quick runs:

```sh
export TWI_DEFAULT_CHANNELS="somechannel"
export TWI_ANIMATION_MODE="fast"
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

```sh
go version
go mod tidy
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go tool govulncheck ./...
go tool staticcheck ./...
```

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

Near-term work is focused on keeping the MVP sharp: better credential validation, richer diagnostics, real asset/image rendering, and eventual multi-channel behavior. The source of truth lives in the product docs under `docs/`.
