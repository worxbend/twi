# Twitch Chat TUI

`twi` is a planned terminal user interface for Twitch chat. The goal is a polished, keyboard-first chat client that can read live Twitch chat, send messages, animate incoming messages, and render rich chat content with graceful terminal fallbacks.

This repository is in bootstrap. `PLAN.md` is the current source of truth for product direction, architecture, and delivery phases. The first Go module, CLI/config foundation, a non-network Bubble Tea mock shell, and a one-channel Twitch IRC read path exist.

## Project Goals

- Show Twitch chat messages live with low latency.
- Send messages from an in-terminal composer.
- Provide a colorful Bubble Tea based TUI with status, chat viewport, composer, and help.
- Support typed-in reveal animation for new messages without corrupting Unicode, styling, or image placeholders.
- Render usernames, badges, avatars, Twitch emotes, standard emoji, replies, notices, and moderation events clearly.
- Support inline images in capable terminals while keeping text and initials fallbacks usable everywhere.
- Keep Twitch credentials secure across flags, environment variables, config files, logs, and diagnostics.

## Current Bootstrap Status

- The repository contains planning docs, a Go module, a `cmd/twi` entrypoint, config loading, secret redaction, a diagnostic skeleton, a deterministic Bubble Tea mock chat shell, and a live Twitch IRC read adapter.
- The mock shell supports terminal resize, `tab` focus switching between chat and composer, `?` help expansion, page-key chat scrolling, a reduced narrow-width layout, and tick-driven reveal animation for incoming mock messages.
- `twi chat --channel <channel>` starts the same Bubble Tea shell against Twitch IRC when `TWI_TWITCH_USERNAME` and `TWI_TWITCH_OAUTH_TOKEN` are configured. The token must be an IRC OAuth token with `chat:read`; sending from the composer also needs `chat:edit`.
- The current stable Go version was verified from the official Go downloads page as `go1.26.4` on 2026-07-01.
- The module uses Go `1.26` semantics, `toolchain go1.26.4`, and module-managed `govulncheck`/`staticcheck` tools.
- Inline images, reply sends, and `/me` composer actions are not implemented yet.

## Planned CLI

The expected binary name is `twi`.

| Command | Status | Purpose |
| --- | --- | --- |
| `twi chat --mock` | Current bootstrap | Start a deterministic non-network Bubble Tea mock chat shell without Twitch credentials. |
| `twi chat --channel <channel>` | Current bootstrap | Start the TUI for one Twitch channel using Twitch IRC when username and OAuth token are configured. |
| `twi chat --channel <one> --channel <two>` | Future | Start multi-channel mode. |
| `twi login` | Future | Start an OAuth or setup flow. |
| `twi config show` | Current bootstrap | Print effective configuration with secrets redacted. |
| `twi config path` | Current bootstrap | Print the config file path. |
| `twi doctor` | Current bootstrap | Print early local checks. Twitch reachability, token validation, cache access, and image protocol checks are future work. |

## MVP Scope

The first runnable milestone should provide:

- A Go module and `cmd/twi` entrypoint. Current.
- CLI help. Current.
- Config loading from flags, environment variables, and a config file. Current bootstrap.
- Secret redaction utilities. Current bootstrap.
- A Bubble Tea root model with status bar, chat viewport, composer input, focus handling, viewport scrolling, compact/expanded help, and narrow-width layout. Current.
- A fake chat source for development. Current app boundary and deterministic fake exist.
- `twi chat --mock` with animated mock messages. Current.
- `twi chat --channel <name>` live IRC read path for one configured channel. Current.
- Composer sends for the active live channel with visible queued/sent/failed/rate-limited status. Current.

## Future Scope

Later milestones add:

- Reply sends, `/me`, richer reconnect handling, and live credential/scope validation.
- Helix-backed identity and asset lookups for avatars, emotes, badges, and token validation.
- Kitty/Ghostty inline image rendering with fallback text, Unicode, and initials.
- Multi-channel navigation, unread counts, message selection, inspect panel, mouse support, diagnostics, and richer setup flows.

## Development Commands

These are the intended commands once the Go module is initialized:

```sh
go version
go mod tidy
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
```

Pinned module tools:

```sh
go tool govulncheck ./...
go tool staticcheck ./...
```

Use writable caches in restricted environments when the default Go cache is outside the workspace:

```sh
GOTOOLCHAIN=local GOCACHE=/tmp/twi-gocache GOMODCACHE=/tmp/twi-gomodcache go test ./...
GOTOOLCHAIN=local GOCACHE=/tmp/twi-gocache GOMODCACHE=/tmp/twi-gomodcache STATICCHECK_CACHE=/tmp/twi-staticcheck-cache go tool staticcheck ./...
```

`GOTOOLCHAIN=auto` remains the normal developer setting; `GOTOOLCHAIN=local` is useful only when automatic toolchain download is not available.

## Secret Handling

Never commit Twitch OAuth tokens, client secrets, `.env` files, local config files with credentials, logs, terminal recordings, or screenshots that expose secrets.

The planned implementation must not print secrets in:

- `twi config show`
- `twi doctor`
- logs
- errors
- test snapshots
- debug panels

Prefer environment variables or a local config file with restrictive permissions until secure token storage is implemented.

## More Documentation

- [Authentication](docs/auth.md)
- [Configuration](docs/config.md)
- [Development](docs/development.md)
- [Terminal Images](docs/terminal-images.md)
