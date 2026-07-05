# Contributing to twi

Thanks for helping make `twi` a sharp terminal Twitch client. This project values small, verifiable changes, plain Go, careful secret handling, and documentation that says exactly what the app can do today.

## Support Boundary

`twi` targets Unix-like terminals and Docker. Saved credentials are supported only on Go `unix` builds through the restrictive credential-file store. Windows is not a supported target, and non-Unix saved credentials must keep failing closed.

## First Local Run

Start from a clean checkout and keep Twitch credentials out of the default environment while running repository checks:

```sh
export GOTOOLCHAIN=auto TERM=xterm-256color
export XDG_CONFIG_HOME="$(mktemp -d)" XDG_CACHE_HOME="$(mktemp -d)"
export TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN=
export TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET=
export TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN=
export TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET=
go run ./cmd/twi chat --mock --channel demo
```

The mock path is the safest place to inspect UI behavior because it needs no Twitch account, no token, and no network.

## Contribution Flow

1. Read [README.md](README.md), [docs/index.md](docs/index.md), [docs/code-style.md](docs/code-style.md), and [SECURITY.md](SECURITY.md).
2. Pick one coherent behavior, documentation, or test improvement.
3. Keep package boundaries intact. UI code belongs in `internal/app`; Twitch transport and Helix adapters belong in `internal/twitch`; rendering belongs in `internal/render`; secret storage and cache persistence belong in `internal/storage`.
4. Add or update tests near the changed package.
5. Update docs when behavior, setup, architecture, support boundaries, or verification changes.
6. Run the relevant focused checks, then the broad gate when practical.

## Default Verification

Run this before sending a PR when the change is not trivial:

```sh
go mod tidy
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go tool govulncheck ./...
go tool staticcheck ./...
go build -o /tmp/twi-validation ./cmd/twi
go run ./cmd/twi --help
go run ./cmd/twi chat --mock --channel example
go run ./cmd/twi doctor
go run ./cmd/twi config show
git diff --check
```

Use isolated `XDG_CONFIG_HOME` and `XDG_CACHE_HOME` directories and clear all `TWI_*` and `TWITCH_*` credential variables for credential-free validation. Do not let tests or smoke commands read your real local Twitch config by accident.

## Manual Checks

Use [docs/manual-validation.md](docs/manual-validation.md) as the evidence log for terminal behavior that automated tests cannot prove. Record terminal name/version, viewport size, command, observed behavior, and skipped checks with the exact environment reason. Credentialed Twitch chat, real Kitty/Ghostty inline image drawing, and exact Docker daemon checks are environment-dependent and must not be claimed without evidence.

## Secret Handling

Never commit, paste, log, screenshot, or record Twitch OAuth tokens, refresh tokens, client secrets, callback codes, OAuth state, authorization URLs, private config files, credential files, or debug logs that contain private context.

Use redaction helpers for every user-facing error, diagnostic line, debug record, and test fixture that can touch auth or config data. Debug logs must use curated fields; do not dump raw IRC tags, raw transport errors, raw config structs, source URLs, or credential-shaped values.

## Pull Request Checklist

- The change is scoped to one behavior or documentation improvement.
- Tests cover the changed behavior or the manual evidence explains why automation is not possible.
- `go fmt`, relevant `go test`, and `git diff --check` pass.
- New docs link to related docs with relative `.md` paths.
- README, docs, and code comments do not overclaim Windows, credentialed Twitch, Docker, or Kitty/Ghostty support.
- No real secrets appear in commits, logs, fixtures, screenshots, or copied output.
