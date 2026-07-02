# Docker Guide

`twi` is a terminal app. Docker packaging is useful for repeatable builds, smoke tests, and running the same binary on a host that has Docker installed. Interactive chat still needs a real TTY.

## Build The Image

```sh
docker build -t twi:local .
```

The image uses a multi-stage build:

- `golang:1.26.4-bookworm` compiles `cmd/twi`.
- `debian:bookworm-slim` runs the static binary as a non-root `twi` user.
- CA certificates are copied into the runtime image for Twitch IRC over TLS.

## Run Mock Chat

Mock chat is ready today and does not need Twitch credentials or network access.

```sh
docker run --rm -it twi:local chat --mock --channel demo
```

With Compose:

```sh
docker compose run --rm mock
```

## Run Live Chat

Live chat is partially shipped for one Twitch IRC channel. It can read, send,
reply, and send `/me` actions when username/token credentials are configured,
but login/setup, multi-channel routing, and Helix-backed token validation are
still planned.

Username/token credentials currently come from environment variables or the
flat config file. Docker examples pass them through environment variables; CLI
flags currently override channel and config path only.

Set credentials in your shell:

```sh
export TWITCH_USERNAME="your_twitch_login"
export TWITCH_ACCESS_TOKEN="<your-twitch-access-token>"
export TWITCH_CHANNEL="somechannel"
```

Then run:

```sh
docker run --rm -it \
  -e TWITCH_USERNAME \
  -e TWITCH_ACCESS_TOKEN \
  twi:local chat --channel "$TWITCH_CHANNEL"
```

With Compose:

```sh
cp .env.example .env
docker compose run --rm live
```

`compose.yaml` reads `TWITCH_*` variables from your shell or local `.env`. Keep real secrets out of tracked files. If `TWITCH_ACCESS_TOKEN` does not include the `oauth:` prefix, `twi` adds it before opening Twitch IRC.

When `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`, and `TWITCH_REFRESH_TOKEN` are set, live chat will try one in-memory token refresh if Twitch IRC rejects the access token during login.

## Run Doctor

```sh
docker run --rm twi:local doctor
docker compose run --rm doctor
```

`doctor` diagnostics are partially shipped: they report credential presence,
required-scope hints, Twitch IRC reachability, terminal hints, Kitty/Ghostty
signals, cache writability, and feature modes. Token identity, ownership,
expiry, and exact scope validation are planned. `doctor` is safe to share only
after you personally review the output. It redacts OAuth tokens and client
secrets, but local paths and terminal details may still be private.

## Use A Mounted Config File

The runtime image sets:

```text
XDG_CONFIG_HOME=/config
XDG_CACHE_HOME=/cache
```

That means the default config path inside the container is:

```text
/config/twi/config.toml
```

Example:

```sh
mkdir -p .local/twi-config/twi .local/twi-cache
$EDITOR .local/twi-config/twi/config.toml

docker run --rm -it \
  -v "$PWD/.local/twi-config:/config" \
  -v "$PWD/.local/twi-cache:/cache" \
  twi:local chat --channel somechannel
```

Do not commit `.local`, config files with credentials, shell history containing tokens, or exported logs.

## Deploy Notes

For `twi`, deployment is packaging rather than running a background web service.

Reasonable deployment options:

- Copy the `twi` binary to a workstation or jumpbox where a human will use it.
- Build and push the Docker image to a private registry.
- Run the container with `-it` on the machine where the terminal UI should appear.

Avoid:

- Baking OAuth tokens into the image.
- Running live chat without a TTY.
- Treating the current app as a daemon or server. That is not its shape today.

## Image Commands

Show CLI help:

```sh
docker run --rm twi:local --help
```

Show redacted config:

```sh
docker run --rm \
  -e TWITCH_USERNAME \
  -e TWITCH_ACCESS_TOKEN \
  twi:local config show
```

Run mock mode with animation disabled:

```sh
docker run --rm -it \
  -e TWI_ANIMATION_MODE=off \
  twi:local chat --mock --channel demo
```
