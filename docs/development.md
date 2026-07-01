# Development

This document summarizes the development workflow and architecture for `twi`. It describes the current MVP implementation plus planned extension points.

## Current State

- `PLAN.md` is the source of truth for architecture, milestones, and quality gates.
- The current stable Go version was verified as `go1.26.4` on 2026-07-01.
- The Go module uses `go 1.26` and `toolchain go1.26.4`.
- `govulncheck` and `staticcheck` are pinned as Go module tools.
- Use Go modules only. Do not use GOPATH workflows.
- A CLI/config foundation exists with a deterministic non-network Bubble Tea mock shell, a one-channel live Twitch IRC read path, active-channel composer sends, replies, `/me` action sends, and no-image asset fallbacks.
- The shell handles resize, chat/composer focus via `tab`, expanded help via `?`, page-key viewport scrolling, selected-message reply mode with `up`/`down` and `r`, `esc` reply cancellation, composer text entry, Enter-to-send for live clients, reduced narrow-width status/help text, send status feedback, and tick-driven reveal animation for scheduled incoming mock messages.
- `internal/app` owns the UI-facing chat boundary, deterministic fake chat client, live transport adapter, and Bubble Tea shell; the app layer consumes normalized `internal/twitch` messages instead of concrete Twitch transport types.
- `internal/twitch` owns the `go-twitch-irc` client wrapper and callback normalization for `PRIVMSG`, `NOTICE`, `USERNOTICE`, `ROOMSTATE`, `CLEARCHAT`, `CLEARMSG`, `USERSTATE`, reconnect, connect, disconnect, and TODO-backed raw fallback events. Raw IRC tags are retained only for diagnostics/debug views.
- `internal/render` converts normalized messages into width-bounded rows of semantic fragments for avatars, timestamps, badges, usernames, replies, notices, actions, deleted messages, mentions, emoji fallbacks, and Twitch emote-token fallbacks. Asset fallback fragments can reserve stable cell widths without loading image data.
- `internal/storage` defines the context-aware asset cache boundary and an in-memory test cache. Current app views do not read the cache or invoke network/file-backed asset loading.
- `internal/animation` turns rendered rows into grapheme-safe reveal units and maintains a deterministic bounded reveal queue for `off`, `reduced`, and `fast` animation modes. `internal/app` owns the Bubble Tea tick commands that enqueue incoming mock messages and advance active reveals.
- `internal/theme` owns palette data and contrast correction for user-supplied foreground colors before render fragments are styled.

## Architecture Lanes

The planned package boundaries are:

```text
cmd/twi        CLI entrypoint
internal/app  Bubble Tea model, update, commands, views, keys, styles
internal/config flags, env, config files, auth settings, redaction
internal/twitch Twitch IRC, Helix wrappers, normalized protocol messages
internal/render message fragments, wrapping, badges, mentions, replies
internal/theme reusable palettes and styles
internal/storage cache for metadata and assets
internal/assets asset resolution and image cache behavior
internal/animation typed-in reveal scheduler and timing
```

Keep boundaries strict:

- UI should depend on internal interfaces, not Twitch library types.
- Twitch/network code should not depend on Bubble Tea components.
- Rendering should consume normalized messages and assets, not raw IRC strings.
- Animation should consume render rows/fragments, not raw strings; queue overflow completes the oldest active reveal immediately so callers can render it statically.
- Image loading and network work must not block Bubble Tea `Update` or `View`; current asset fallback rendering is pure row construction, and future cache/image work should flow through commands/messages.

## Core Interfaces

The plan calls for interfaces around:

- `ChatClient`
- `MessageStream`
- `Sender`
- `IdentityAssetClient`
- `AssetCache`
- `ImageRenderer`
- `AnimationClock`

`internal/app.ChatClient` currently combines the app-facing message stream, connection-state stream, and send contract. Send results can carry accepted, failed, or rate-limit-like feedback so the composer can clear accepted sends and restore unsent text on failure. `internal/storage.AssetCache` provides context-aware `GetAsset`/`PutAsset` methods, and `internal/render.ImageRenderer` describes fixed-cell image rendering for asynchronous callers. Use `internal/app.FakeChatClient` and `internal/storage.MemoryAssetCache` for deterministic tests. Use additional test fakes for network, asset, image, and animation behavior.

## Tooling

Expected baseline commands:

```sh
go version
go mod tidy
go fmt ./...
go vet ./...
go test ./...
```

Use race testing for concurrency, networking, cache, and animation scheduler changes:

```sh
go test -race ./...
```

Pinned module tool checks:

```sh
go tool govulncheck ./...
go tool staticcheck ./...
```

In restricted environments where the default module cache is read-only, use writable caches under `/tmp` and `GOTOOLCHAIN=local` for local verification. `staticcheck` also needs a writable cache, for example `STATICCHECK_CACHE=/tmp/twi-staticcheck-cache`. Normal developer environments should keep `GOTOOLCHAIN=auto`.

## Dependency Rules

- Prefer the standard library or existing dependency set before adding a new dependency.
- Add dependencies with `go get <module>@latest`.
- Review selected versions and transitive impact.
- Keep `go.mod` and `go.sum` machine-managed through `go get`, `go mod tidy`, and `go mod edit`.
- Use Go 1.24+ `tool` directives for project tools instead of unmanaged global binaries.

Planned primary dependencies:

- Bubble Tea for the application loop.
- Bubbles for viewport, textarea, spinner, help, list, table, and related components.
- Lip Gloss for layout and styling.
- go-twitch-irc for the MVP Twitch IRC transport.
- Helix client for user, avatar, emote, badge, and token validation APIs.
- kittyimg for Kitty-compatible terminal image rendering.

## Testing Strategy

Unit coverage should include:

- Config precedence.
- Secret redaction.
- Twitch message normalization.
- IRC emote position parsing.
- Emoji grapheme detection.
- Avatar, badge, emote, and cache behavior.
- Width-aware wrapping.
- Grapheme-safe message reveal.
- Animation degradation under high throughput.
- Key bindings.
- Resize and focus layout behavior.
- Send queue and rate-limit behavior.

Integration coverage should include:

- Fake Twitch chat client feeding messages into Bubble Tea.
- Fake send path with success, failure, context cancellation, replies, actions, and rate-limit-like responses.
- Reconnect state transitions.

Golden or snapshot coverage should include:

- Narrow and wide layouts.
- Normal messages, mentions, replies, `/me`, notices, deleted messages, badges, fallback emotes, fallback emoji, image placeholders, and partial reveal frames.

Manual verification should include:

- `twi chat --mock`.
- `twi doctor`.
- A low-traffic Twitch channel.
- Sending a test message.
- Terminal resize while connected.
- Kitty/Ghostty image mode and non-Kitty fallback mode.
- Reduced/off animation modes.

## Quality Gates

Before handoff, run the narrowest relevant checks and inspect the diff. For feature work, prefer ending with:

```sh
go fmt ./...
go vet ./...
go test ./...
```

When relevant:

```sh
go test -race ./...
go tool govulncheck ./...
```

Also check:

- No secret leakage.
- No blocking I/O in `View`.
- No raw byte/rune slicing of user-visible Unicode content.
- Bounded animation queues.
- Async asset downloads.
- Text fallbacks for terminal image features.
- Docs match actual CLI behavior.

## Agent Task Shape

Use one focused task at a time:

```text
Task:
Owner lane:
Goal:
Context:
Files likely touched:
Implementation notes:
Acceptance criteria:
Verification:
Risks:
Follow-ups:
```

Prefer vertical slices that end in runnable behavior. Keep docs updated when behavior changes, especially around auth, config, terminal images, and command availability.
