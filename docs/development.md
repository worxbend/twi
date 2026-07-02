# Development

This document summarizes the development workflow and architecture for `twi`. It describes the current MVP implementation plus planned extension points.

## Current State

- `PLAN.md` is the source of truth for architecture, milestones, and quality gates.
- The current stable Go version was verified as `go1.26.4` on 2026-07-01.
- The Go module uses `go 1.26` and `toolchain go1.26.4`.
- `govulncheck` and `staticcheck` are pinned as Go module tools.
- Use Go modules only. Do not use GOPATH workflows.
- Ready behavior: a deterministic non-network Bubble Tea mock shell, config path/show commands, and text/initials/Unicode/badge/emote-token fallback rendering.
- Partially shipped behavior: multi-channel live Twitch IRC read/send with active-channel composer sends, selected-message replies and inspect diagnostics, `/me` action sends, keyboard-first channel sidebar, command palette, optional mouse support, Twitch avatar metadata, Twitch emote/badge metadata resolution, standard emoji provider metadata, visible-row asset event integration, bounded PNG/JPEG/GIF image decode and cell preparation, inline image renderer plumbing, and `twi doctor` diagnostics for credential presence, Twitch OAuth identity/expiry/scope validation, refresh availability, username mismatch, Twitch reachability, terminal hints, Kitty/Ghostty signals, cache writability, and feature modes.
- Planned behavior: `twi login`, setup wizard, secure credential storage, default live asset resolver/downloader/preparer/renderer wiring, and manual Kitty/Ghostty inline image validation.
- Twitch username/token credentials currently come from environment variables or the flat config file; CLI flags currently override channels and config path only.
- The shell handles resize, chat/composer focus via `tab`, channel switching via `[`/`]`, a normal/wide channel sidebar with unread counts and connection indicators, collapsed narrow-width channel status, command palette via `ctrl+p`, expanded help via `?`, page-key viewport scrolling, selected-message reply mode with `up`/`down` and `r`, selected-message inspect with `i`, `esc` inspect/reply cancellation, optional mouse wheel/sidebar/composer/message interactions, composer text entry, Enter-to-send for live clients, reduced narrow-width status/help text, send status feedback, and tick-driven reveal animation for scheduled incoming mock messages.
- `internal/app` owns the UI-facing chat boundary, deterministic fake chat client, live transport adapter, and Bubble Tea shell; the app layer consumes normalized `internal/twitch` messages instead of concrete Twitch transport types.
- `internal/twitch` owns the `go-twitch-irc` client wrapper, Twitch OAuth token validation adapter, Helix Get Users avatar metadata adapter, Helix chat emote/badge metadata adapter, and callback normalization for `PRIVMSG`, `NOTICE`, `USERNOTICE`, `ROOMSTATE`, `CLEARCHAT`, `CLEARMSG`, `USERSTATE`, reconnect, connect, disconnect, and TODO-backed raw fallback events. Raw IRC tags are retained only for diagnostics/debug views.
- `internal/render` converts normalized messages into width-bounded rows of semantic fragments for avatars, timestamps, badges, usernames, replies, notices, actions, deleted messages, mentions, emoji fallbacks, and Twitch emote-token fallbacks. Asset fallback fragments can reserve stable cell widths and carry resolved asset refs without loading image data; standard emoji fallback text stays native Unicode while emoji asset refs use provider-neutral lowercase hex codepoint IDs from `internal/assets`. The image preparation boundary decodes bounded downloaded PNG, JPEG, and first-frame GIF assets, crops/scales them to the requested cell rectangle, writes renderer-ready PNG files, and rejects unsafe paths, unsupported media, corrupt data, and oversized images without leaking credential-like values.
- `internal/storage` defines the context-aware asset cache boundary, an in-memory test cache, a disk-backed asset cache for metadata, public source URLs, and bytes under the platform cache directory, TTL/size pruning, and filesystem probes used by diagnostics.
- `internal/assets` defines the async asset resolver boundary for avatar identity lookup, batched avatar metadata resolution, Twitch emote/badge metadata resolution, standard emoji provider metadata resolution, emote/emoji/badge metadata lookup, downloading, cache lookup/write-through, context cancellation, and app-facing asset events. Live chat can debounce visible author avatar lookups through `AvatarBatchResolver` and can feed visible-row asset events into fixed-width image cell preparation; render/view paths still do not perform network or file I/O.
- `internal/animation` turns rendered rows into grapheme-safe reveal units and maintains a deterministic bounded reveal queue for `off`, `reduced`, and `fast` animation modes. `internal/app` owns the Bubble Tea tick commands that enqueue incoming mock messages, advance active reveals, and refresh active reveal rows when layout-stable asset cells arrive.
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
- Image loading and network work must not block Bubble Tea `Update` or `View`; asset fallback rendering is pure row construction, and avatar metadata/cache plus visible asset preparation/rendering work flows through debounced commands that merge refs or prepared cells without changing fallback row widths. Permanent image preparation/render failures are recorded only with URL-free asset and downloaded-record identity plus cell dimensions; transient resolver, downloader, cache, filesystem, and context failures stay on the retry path.

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
- `ImagePreparer`
- `ImageRenderer`
- `AnimationClock`

`internal/app.ChatClient` currently combines the app-facing message stream, connection-state stream, and send contract. Send results can carry accepted, failed, or rate-limit-like feedback so the composer can clear accepted sends and restore unsent text on failure. Live messages, notices, drafts, replies, send state, unread counts, and scroll offsets route through per-channel state keyed by normalized channel name; outbound sends use the currently active channel. The command palette is model-local modal state: `ctrl+p` opens it, typed input filters deterministic commands, `enter` executes the selected command, and `esc` closes it without mutating composer drafts, reply context, or selected-message state. Palette actions are backed by the same keyboard-accessible handlers as normal input where practical, including help/inspect toggles, focus, reply, scroll, channel switching, active-channel local clear, and reconnect requests through the optional app-side reconnect capability. When a client does not implement that optional capability, the model reports reconnect as unavailable instead of leaving the channel in a reconnecting state. The inspect panel reads the existing selected-message ID, displays normalized message, author, badge, and raw tag diagnostics, and redacts credential-shaped values before fitting lines. Twitch IRC connect/reconnect/disconnect events are connection-level callbacks, so the app copies those states onto configured channels instead of treating them as independent per-channel transport events. `internal/app.AvatarResolver` lets the shell debounce visible author avatar metadata work behind an app-facing interface. `internal/app.ClientOptions` can also provide an `assets.EventResolver`, `render.ImagePreparer`, and `render.ImageRenderer`; the shell schedules visible avatar, badge, emote, and emoji asset requests from Bubble Tea commands, prepares downloaded records before rendering when a preparer is installed, stores prepared cells by URL-free asset key, and refreshes static or active visible rows without resetting scroll, reply, or composer state. `internal/storage.AssetCache` provides context-aware `GetAsset`/`PutAsset` methods; `internal/storage.DiskAssetCache` persists URL-free metadata, public source URLs, and cache-owned bytes using deterministic hashed paths, and exposes explicit pruning for expired records plus oldest-first size reduction; `internal/assets.AvatarBatchResolver` batches Helix-style user lookups and caches `profile_image_url` metadata; `internal/assets.TwitchMetadataResolver` resolves Twitch emote and badge IDs into cached public image refs without changing fallback text; `internal/assets.EmojiAssetKey` maps standard emoji grapheme clusters to URL-free provider-neutral asset keys; `internal/assets.EmojiMetadataProvider` maps those emoji keys to public provider URLs and metadata-only cache records; `internal/assets.Resolver` composes identity lookup, metadata lookup, downloading, and cache write-through into typed `assets.Event` results for asynchronous app commands; `internal/render.ImagePreparer` describes bounded decode/crop/scale normalization into renderer-ready PNG records; `internal/render.ImageRenderer` describes fixed-cell image rendering for asynchronous callers; and `internal/render.KittyRenderer` produces fallback-safe Kitty graphics output for prepared cached PNG assets in supported terminals. Chat row generation can attach prepared image cells by URL-free asset key while `Row.Plain` preserves text fallbacks and `Row.TerminalString` substitutes only cells that match the reserved width. Use `internal/app.FakeChatClient`, `internal/storage.MemoryAssetCache`, and fake `internal/assets` lookup/downloader dependencies for deterministic tests.

## Tooling

Pull requests run the repository-native Go gate through GitHub Actions. Run the
same command set from a clean checkout before opening or updating a PR:

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

The empty Twitch credential environment variables plus isolated
`XDG_CONFIG_HOME` and `XDG_CACHE_HOME` directories keep smoke checks independent
from local config files, secrets, or a Twitch account. Credentialed Twitch chat,
Docker build/runtime checks, and Kitty/Ghostty inline-image validation are
manual or release-specific checks. Replace `origin/main` with the PR base branch
when needed; use plain `git diff --check` for uncommitted local changes.

Task-specific smoke and metadata checks used by recent iterations:

```sh
go run ./cmd/twi --help
go run ./cmd/twi chat --mock --channel example
go run ./cmd/twi chat --mock --channel one --channel two
go run ./cmd/twi doctor
go run ./cmd/twi config show
go run ./cmd/twi chat --channel example
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
