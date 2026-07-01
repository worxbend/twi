# Twitch Chat TUI

`twi` is a terminal user interface for Twitch chat. The goal is a polished, keyboard-first chat client that can read live Twitch chat, send messages, animate incoming messages, and render rich chat content with graceful terminal fallbacks.

`PLAN.md` is the current source of truth for product direction, architecture, and delivery phases. The Go module, CLI/config foundation, non-network Bubble Tea mock shell, one-channel Twitch IRC read/send path, fallback rendering contracts, and diagnostics are implemented.

## Project Goals

- Show Twitch chat messages live with low latency.
- Send messages from an in-terminal composer.
- Provide a colorful Bubble Tea based TUI with status, chat viewport, composer, and help.
- Support typed-in reveal animation for new messages without corrupting Unicode, styling, or image placeholders.
- Render usernames, badges, avatars, Twitch emotes, standard emoji, replies, notices, and moderation events clearly.
- Support inline images in capable terminals while keeping text and initials fallbacks usable everywhere.
- Keep Twitch credentials secure across environment variables, config files, logs, and diagnostics.

## Current Status

- The repository contains planning docs, a Go module, a `cmd/twi` entrypoint, config loading, secret redaction, diagnostics, a deterministic Bubble Tea mock chat shell, and a live Twitch IRC read/send adapter.
- The mock shell supports terminal resize, `tab` focus switching between chat and composer, `?` help expansion, page-key chat scrolling, selected-message reply mode, `/me` action sends, a reduced narrow-width layout, and tick-driven reveal animation for incoming mock messages.
- `twi chat --channel <channel>` starts the same Bubble Tea shell against Twitch IRC when `TWI_TWITCH_USERNAME` and `TWI_TWITCH_OAUTH_TOKEN` are configured. The token must be an IRC OAuth token with `chat:read`; sending from the composer also needs `chat:edit`.
- The current stable Go version was verified from the official Go downloads page as `go1.26.4` on 2026-07-01.
- The module uses Go `1.26` semantics, `toolchain go1.26.4`, and module-managed `govulncheck`/`staticcheck` tools.
- Inline image loading and terminal image drawing are not implemented yet; current rendering uses stable text, initials, Unicode, badge, and emote-token fallbacks.

## CLI

The binary name is `twi`.

| Command | Status | Purpose |
| --- | --- | --- |
| `twi chat --mock [--channel <channel>]` | Current | Start a deterministic non-network Bubble Tea mock chat shell without Twitch credentials. |
| `twi chat --channel <channel>` | Current | Start the TUI for one Twitch channel using Twitch IRC when username and OAuth token are configured. |
| `twi chat --channel <one> --channel <two>` | Future | Start multi-channel mode. |
| `twi login` | Future | Start an OAuth or setup flow. |
| `twi config show` | Current | Print effective configuration with secrets redacted. |
| `twi config path` | Current | Print the config file path. |
| `twi doctor` | Current | Print secret-safe config, credential, token-status, Twitch reachability, terminal, image-signal, cache, and feature-mode diagnostics. |

## MVP Scope

The first runnable milestone should provide:

- A Go module and `cmd/twi` entrypoint. Current.
- CLI help. Current.
- Config loading from flags, environment variables, and a config file. Current.
- Secret redaction utilities. Current.
- A Bubble Tea root model with status bar, chat viewport, composer input, focus handling, viewport scrolling, compact/expanded help, and narrow-width layout. Current.
- A fake chat source for development. Current app boundary and deterministic fake exist.
- `twi chat --mock` with animated mock messages. Current.
- `twi chat --channel <name>` live IRC read path for one configured channel. Current.
- Composer sends for the active live channel with visible queued/sent/failed/rate-limited status, replies, and `/me` actions. Current.

## Future Scope

Later milestones add:

- Richer reconnect handling and Helix-backed live credential/scope validation.
- Helix-backed identity and asset lookups for avatars, emotes, and badges.
- Kitty/Ghostty inline image rendering with fallback text, Unicode, and initials.
- Multi-channel navigation, unread counts, inspect panel, mouse support, and richer setup flows.

## Known Limitations

- Live Twitch chat currently supports one configured channel. Passing multiple live channels returns an actionable error.
- `twi login` is not implemented. Configure credentials with environment variables or the flat config file.
- `twi doctor` names the required `chat:read` and `chat:edit` IRC scopes, but real token identity, expiry, and scope validation are not wired to Helix yet.
- Inline images are fallback-only. The renderer reserves stable cells for avatars, badges, emoji, and emote tokens, but it does not download assets or draw Kitty/Ghostty images.
- Manual live read/send validation still requires user-owned Twitch credentials and a channel where the account can chat.

## Development Commands

These are the standard validation commands:

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

The implementation must not print secrets in:

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
