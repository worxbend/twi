# Development

This document summarizes the development workflow and architecture for `twi`. It describes the current MVP implementation plus planned extension points.

## Current State

- `PLAN.md` is the source of truth for architecture, milestones, and quality gates.
- The current stable Go version was verified as `go1.26.4` on 2026-07-01.
- The Go module uses `go 1.26` and `toolchain go1.26.4`.
- `govulncheck` and `staticcheck` are pinned as Go module tools.
- Use Go modules only. Do not use GOPATH workflows.
- Ready behavior: a deterministic non-network Bubble Tea mock shell, config path/show commands, and text/initials/Unicode/badge/emote-token fallback rendering.
- Partially shipped behavior: one-channel live Twitch IRC read/send with active-channel composer sends, selected-message replies, `/me` action sends, Twitch avatar metadata, Twitch emote/badge metadata resolution, and `twi doctor` diagnostics for credential presence, Twitch OAuth identity/expiry/scope validation, refresh availability, username mismatch, Twitch reachability, terminal hints, Kitty/Ghostty signals, cache writability, and feature modes.
- Planned behavior: `twi login`, multi-channel live chat, asset download wiring, live UI integration for emote/badge metadata events, and inline terminal image rendering.
- Twitch username/token credentials currently come from environment variables or the flat config file; CLI flags currently override channel and config path only.
- The shell handles resize, chat/composer focus via `tab`, expanded help via `?`, page-key viewport scrolling, selected-message reply mode with `up`/`down` and `r`, `esc` reply cancellation, composer text entry, Enter-to-send for live clients, reduced narrow-width status/help text, send status feedback, and tick-driven reveal animation for scheduled incoming mock messages.
- `internal/app` owns the UI-facing chat boundary, deterministic fake chat client, live transport adapter, and Bubble Tea shell; the app layer consumes normalized `internal/twitch` messages instead of concrete Twitch transport types.
- `internal/twitch` owns the `go-twitch-irc` client wrapper, Twitch OAuth token validation adapter, Helix Get Users avatar metadata adapter, Helix chat emote/badge metadata adapter, and callback normalization for `PRIVMSG`, `NOTICE`, `USERNOTICE`, `ROOMSTATE`, `CLEARCHAT`, `CLEARMSG`, `USERSTATE`, reconnect, connect, disconnect, and TODO-backed raw fallback events. Raw IRC tags are retained only for diagnostics/debug views.
- `internal/render` converts normalized messages into width-bounded rows of semantic fragments for avatars, timestamps, badges, usernames, replies, notices, actions, deleted messages, mentions, emoji fallbacks, and Twitch emote-token fallbacks. Asset fallback fragments can reserve stable cell widths and carry resolved asset refs without loading image data; standard emoji fallback text stays native Unicode while emoji asset refs use provider-neutral lowercase hex codepoint IDs from `internal/assets`.
- `internal/storage` defines the context-aware asset cache boundary, an in-memory test cache, a disk-backed asset cache for metadata, public source URLs, and bytes under the platform cache directory, TTL/size pruning, and filesystem probes used by diagnostics.
- `internal/assets` defines the async asset resolver boundary for avatar identity lookup, batched avatar metadata resolution, Twitch emote/badge metadata resolution, emote/emoji/badge metadata lookup, downloading, cache lookup/write-through, context cancellation, and app-facing asset events. Live chat can debounce visible author avatar lookups through `AvatarBatchResolver`; render/view paths still do not perform network or file I/O.
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
- Animation should consume render rows/fragments, not raw strings; queue overflow completes the oldest active reveal immediately so callers can render it statically. App views also render new messages statically while the chat viewport is scrolled away from the bottom so off-screen traffic cannot grow a reveal backlog or shift the user's current page.
- Image loading and network work must not block Bubble Tea `Update` or `View`; asset fallback rendering is pure row construction, and avatar metadata/cache work flows through debounced commands that merge asset refs without changing fallback row widths.

## Core Interfaces

The plan calls for interfaces around:

- `ChatClient`
- `MessageStream`
- `Sender`
- `IdentityLookup`
- `MetadataLookup`
- `Downloader`
- `AssetCache`
- `EventResolver`
- `ImageRenderer`
- `AnimationClock`

`internal/app.ChatClient` currently combines the app-facing message stream, connection-state stream, and send contract. Send results can carry accepted, failed, or rate-limit-like feedback so the composer can clear accepted sends and restore unsent text on failure. `internal/app.AvatarResolver` lets the shell debounce visible author avatar metadata work behind an app-facing interface. `internal/storage.AssetCache` provides context-aware `GetAsset`/`PutAsset` methods; `internal/storage.DiskAssetCache` persists URL-free metadata, public source URLs, and cache-owned bytes using deterministic hashed paths, and exposes explicit pruning for expired records plus oldest-first size reduction; `internal/assets.AvatarBatchResolver` batches Helix-style user lookups and caches `profile_image_url` metadata; `internal/assets.TwitchMetadataResolver` resolves Twitch emote and badge IDs into cached public image refs without changing fallback text; `internal/assets.EmojiAssetKey` maps standard emoji grapheme clusters to URL-free provider-neutral asset keys; `internal/assets.Resolver` composes identity lookup, metadata lookup, downloading, and cache write-through into typed `assets.Event` results for asynchronous app commands; `internal/render.ImageRenderer` describes fixed-cell image rendering for asynchronous callers; and `internal/render.KittyRenderer` produces fallback-safe Kitty graphics output for prepared cached PNG assets in supported terminals. Chat row generation can attach prepared image cells by URL-free asset key while `Row.Plain` preserves text fallbacks and `Row.TerminalString` substitutes only cells that match the reserved width. Use `internal/app.FakeChatClient`, `internal/storage.MemoryAssetCache`, and fake `internal/assets` lookup/downloader dependencies for deterministic tests.

## Tooling

The agent loop has been using these validation commands as the exact Go gate:

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

Task-specific smoke and metadata checks used by recent iterations:

```sh
go run ./cmd/twi --help
go run ./cmd/twi chat --mock --channel example
go run ./cmd/twi doctor
go run ./cmd/twi config show
go run ./cmd/twi chat --channel example
jq empty .agent-loop/tasks.json
git diff --check
```

The live chat smoke command is expected to fail safely in environments without
Twitch credentials. It should print redacted guidance for
`TWI_TWITCH_USERNAME`, `TWI_TWITCH_OAUTH_TOKEN`, `--mock`, `chat:read`, and
`chat:edit`, and it should not attempt networking when credentials are absent.

Focused review searches used by the loop:

```sh
rg "go-twitch-irc|helix" internal/app
rg "os\\.Open|http\\.Get|ReadFile|WriteFile" internal/app internal/render
if rg -n "[ \t]+$" PLAN.md .agent-loop/tasks.json .agent-loop/memory.md README.md docs; then exit 1; else exit 0; fi
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
- Helix-compatible adapters for user/avatar metadata and Twitch emote/badge metadata.
- kittyimg for Kitty-compatible terminal image rendering.

## Testing Strategy

Unit coverage should include:

- Config precedence.
- Secret redaction.
- Token validation outcomes through `internal/twitch.FakeTokenValidator` and fake HTTP tests for `internal/twitch.OAuthTokenValidator`.
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
