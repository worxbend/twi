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

For the release packaging path, prefer the repository dry-run:

```sh
scripts/release-dry-run.sh --out /tmp/twi-release --image twi:local
```

It derives the Go version from `go.mod`, builds release binaries and checksums,
builds this Docker image, and smokes help, doctor, and mock chat with credential
environment variables cleared and host config isolated. See
[Release Packaging](release.md).

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

Live chat is partially shipped for configured Twitch IRC channels. It can read, send,
reply, and send `/me` actions when username/token credentials are configured.
The keyboard-first channel sidebar, command palette, selected-message inspect
panel, and optional mouse controls are current app behavior. `twi setup` writes
non-secret config values and can hand off to login. In the Linux container,
`twi login` is an OAuth browser/callback flow that saves returned tokens
through the restrictive credential-file fallback without printing them;
manual reconnect restarts the live IRC transport while preserving channel UI
state. Refresh-token persistence after IRC reconnect is current when the
supported credential store is available; manual Kitty/Ghostty image validation
is still environment-dependent. Live image asset
wiring is current when config, credentials, cache, and terminal checks allow
it.

Username/token credentials currently come from environment variables, the flat
config file, or saved credentials on supported Unix platforms. The Linux
container uses the restrictive Unix credential-file fallback. Docker examples
pass credentials through environment variables; CLI flags currently override
channel and config path only. Environment and flat config values take
precedence over saved credentials.

Set credentials in your shell:

```sh
export TWITCH_USERNAME="your_twitch_login"
export TWITCH_ACCESS_TOKEN="<oauth token from Twitch>"
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

When `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`, and `TWITCH_REFRESH_TOKEN` are set, live chat will try one token refresh if Twitch IRC rejects the access token during login. In Linux containers with a writable private config mount, refreshed tokens can be saved through the Unix credential file; if saving is unavailable or fails, the refreshed tokens stay in memory for the current chat process and a redacted warning is shown.

## Run Doctor

```sh
docker run --rm twi:local doctor
docker compose run --rm doctor
```

`doctor` diagnostics report credential presence, Twitch OAuth
identity/expiry/scope validation, refresh availability, username mismatch,
Twitch IRC reachability, terminal hints, Kitty/Ghostty signals, cache
writability/pruning, image capability, live image-stack readiness, and feature
modes. `doctor` is safe to share only after you personally review the output. It
redacts OAuth tokens and client secrets, but local paths and terminal details
may still be private.

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

You can create a non-secret mounted config without a prompt:

```sh
mkdir -p "$PWD/.local/twi-config"
sudo chown 10001:10001 "$PWD/.local/twi-config"
docker run --rm \
  -v "$PWD/.local/twi-config:/config" \
  twi:local setup --non-interactive --username your_twitch_login --channel somechannel --login-dry-run
```

The setup command does not write OAuth tokens, refresh tokens, callback codes,
OAuth state, authorization URLs, or client secrets. Use environment variables,
the flat config file, or the private credential file created by `twi login` in
the Linux container for credentials. The container runs as UID/GID `10001`, so
bind-mounted config directories must be writable by that account.

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
