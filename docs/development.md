# Development

This document summarizes the development workflow and architecture for `twi`. It describes the current MVP implementation plus planned extension points. Start with [../CONTRIBUTING.md](../CONTRIBUTING.md) for the contribution flow, [code-style.md](code-style.md) for local code rules, and [architecture.md](architecture.md) for the runtime data-flow map.

## Current State

- `PLAN.md` is the source of truth for architecture, milestones, and quality gates.
- The current stable Go version was verified as `go1.26.4` on 2026-07-01.
- The Go module uses `go 1.26` and `toolchain go1.26.4`.
- `govulncheck` and `staticcheck` are pinned as Go module tools.
- Use Go modules only. Do not use GOPATH workflows.
- Ready behavior: a deterministic non-network Bubble Tea mock shell, config path/show commands, and text/initials/Unicode/badge/emote-token fallback rendering.
- Partially shipped behavior: multi-channel live Twitch IRC read/send with startup token validation when Twitch OAuth validation is reachable, active-channel composer sends, selected-message replies and inspect diagnostics, `/me` action sends, keyboard-first channel sidebar, command palette, per-channel local message filters, focus-aware Twitch event notifications, optional mouse support, and Twitch emote autocomplete backed by Helix global/channel emote metadata. Avatars, badges, Twitch emotes, and standard emoji always render as text (initials chips, badge labels, emote tokens, Unicode glyphs); there is no image decode, download, or terminal-graphics rendering path. Ready diagnostics include opt-in redacted JSON debug logging plus `twi doctor` checks for credential presence, Twitch OAuth identity/expiry/scope validation, refresh availability, username mismatch, Twitch reachability, terminal hints, cache writability, and feature modes.
- Partially shipped auth/setup behavior: `twi setup` writes non-secret config values and can hand off to login; on supported Unix builds, `twi login` runs a browser/local-callback OAuth flow with a `--dry-run` explanation path and saves returned tokens without printing them through the restrictive credential-file fallback. Non-Unix builds keep saved credentials disabled and env/config credentials remain the supported path.
- Current environment-specific manual evidence is recorded in
  [manual-validation.md](manual-validation.md), including checked PTY sizes and
  credential-free smoke paths.
- Twitch username/token credentials currently come from environment variables, the flat config file, or saved credentials on supported Unix platforms; environment and flat config values take precedence over saved credentials, refreshed live IRC tokens are saved through the supported credential store when possible, and CLI flags currently override channels and config path only.
- The shell handles resize, terminal focus reporting, vim-style focus (`i`/`o`/`a` to the composer, `esc` back to chat) plus `tab` cycling through chat/composer/sidebar, a `space` leader chord (`e` sidebar, `c` channel picker, `x` close channel, `i` inspect), runtime channel open/close through the `/channels` picker and sidebar, channel switching via `[`/`]`, active-channel filter toggles via `1` through `4` plus `0` reset, a normal/wide channel sidebar with unread counts, connection indicators, and filter markers, collapsed narrow-width channel status, command palette via `ctrl+p`, expanded help via `?`, page-key viewport scrolling, selected-message reply mode with `up`/`down` or `j`/`k` and `r`, selected-message inspect with `K`, `esc` inspect/reply cancellation, optional mouse wheel/tab-bar/sidebar/overlay/composer/message interactions, composer text entry, Enter-to-send for live clients, local rows for successful sends before any transport echo, reduced narrow-width status/help text, send status feedback, focus-aware system-event notification summaries, and tick-driven reveal animation for scheduled incoming mock messages.
- `internal/app` owns the UI-facing chat boundary, deterministic fake chat client, live transport adapter, focus-aware system notification boundary, and Bubble Tea shell; the app layer consumes normalized `internal/twitch` messages instead of concrete Twitch transport types.
- `internal/twitch` owns the `go-twitch-irc` client wrapper, Twitch OAuth token validation adapter, a Helix Get Users identity adapter, a Helix chat-assets adapter (global/channel emotes and badges, used to back emote autocomplete), and callback normalization for `PRIVMSG`, `NOTICE`, `USERNOTICE`, `ROOMSTATE`, `CLEARCHAT`, `CLEARMSG`, `USERSTATE`, reconnect, connect, disconnect, and TODO-backed raw fallback events. `USERNOTICE` msg-id values such as `raid` are preserved as normalized message system-event IDs for UI decisions. Raw IRC tags are retained only for diagnostics/debug views. Message-level badges and emotes come directly from IRC tags and always render as text; there is no Helix-backed avatar, badge, or emote image lookup for chat rendering.
- `internal/render` converts normalized messages into width-bounded rows of semantic fragments for avatars, timestamps, badges, usernames, replies, notices, actions, deleted messages, mentions, emoji fallbacks, and Twitch emote-token fallbacks. There is no image rendering path: avatars always render as an `[XY]` initials chip (or nothing when `avatar_mode = "off"`), badges as compact text labels, Twitch emotes as their matched text token, and standard emoji as native Unicode text.
- `internal/storage` defines the context-aware asset cache boundary (an in-memory test cache and a disk-backed cache with TTL/size pruning), filesystem probes used by diagnostics, and the credential storage boundary. The asset cache is currently exercised only by `twi doctor`'s asset-cache pruning check; no live rendering path reads or writes through it. Credential storage includes a redacted `CredentialRecord` DTO, test fakes, a versioned fallback JSON format, explicit token reveal helpers, and a restrictive Unix-only fallback file store with exact `0700` directory/`0600` file mode requirements, atomic replacement, symlink rejection, and no-follow file opens. Non-Unix builds disable saved credential persistence.
- `internal/assets` defines `EmoteIndex`, which backs the Ctrl+E searchable emote picker with real Twitch global/channel emote metadata (through a Helix-backed `EmoteLister`) when `emote_autocomplete_mode` and credentials allow it, and a built-in sample list in `--mock` mode. It has no avatar, badge, emoji, or image-download responsibilities.
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
internal/assets emote autocomplete metadata (no image handling)
internal/animation typed-in reveal scheduler and timing
internal/debuglog redacted structured debug records
```

Keep boundaries strict:

- UI should depend on internal interfaces, not Twitch library types.
- Twitch/network code should not depend on Bubble Tea components.
- Rendering should consume normalized messages and assets, not raw IRC strings.
- Debug logging should use `internal/debuglog` and curated fields only; do not
  log raw `ConnectionState`, `ChatMessage`, raw IRC events, transport events,
  send results, raw tag maps, source URLs, or transport errors.
- Animation should consume render rows/fragments, not raw strings; queue overflow completes the oldest active reveal immediately so callers can render it statically. App views also render new messages statically while the chat viewport is scrolled away from the bottom so off-screen traffic cannot grow a reveal backlog or shift the user's current page.
- Network work must not block Bubble Tea `Update` or `View`; asset fallback rendering (avatars, badges, emotes, emoji) is pure, synchronous row construction with no image decode/download/cache step in the render path. Emote autocomplete metadata lookups flow through debounced Bubble Tea commands instead of blocking `Update`/`View`.

## Core Interfaces

The plan calls for interfaces around:

- `ChatClient`
- `MessageStream`
- `Sender`
- `IdentityLookup`
- `MetadataLookup`
- `AssetCache`
- `SystemNotifier`
- `AnimationClock`

`internal/app.ChatClient` currently combines the app-facing message stream, connection-state stream, and send contract. Send results can carry accepted, failed, or rate-limit-like feedback so the composer can clear accepted sends and restore unsent text on failure. Live messages, notices, drafts, replies, send state, unread counts, local filters, and scroll offsets route through per-channel state keyed by normalized channel name; outbound sends use the currently active channel. Local filters are view predicates only: they can narrow the active channel to mentions, broadcaster/mod/VIP rows, notices/system rows, and error-like rows, but they do not delete stored history, unread counts, drafts, selected-message reply context, or send state. Normalized system-event IDs on notice/system messages drive focus-aware notifications: the model emits a `SystemNotifier` command when an event such as a raid arrives for an inactive channel, while the terminal is blurred, or while another in-app panel has focus; the interactive default notifier attempts a dependency-free desktop notification through the host platform (`notify-send`, `osascript`, or PowerShell toast APIs), falls back to a terminal bell, and keeps the latest notification summary visible in the status line. The command palette is model-local modal state: `ctrl+p` opens it, typed input filters deterministic commands, `enter` executes the selected command, and `esc` closes it without mutating composer drafts, reply context, or selected-message state. Palette actions are backed by the same keyboard-accessible handlers as normal input where practical, including help/inspect toggles, focus, reply, scroll, local filter toggles/reset, channel switching, active-channel local clear, and reconnect restart through the optional app-side reconnect capability. When a client does not implement that optional capability, the model reports reconnect as unavailable instead of leaving the channel in a reconnecting state; repeated requests while a restart is running report in-progress feedback. The live IRC client used by CLI startup owns a transport factory, closes the active transport and stream before constructing the replacement, and keeps the same app-facing message/state channels so per-channel history, drafts, reply selection, unread counts, filters, and scroll state survive restart attempts. The inspect panel reads the existing selected-message ID, displays normalized message, author, badge, and raw tag diagnostics, and redacts credential-shaped values before fitting lines. Twitch IRC connect/reconnect/disconnect events are connection-level callbacks, so the app copies those states onto configured channels instead of treating them as independent per-channel transport events. `internal/app.ClientOptions` provides `SystemNotifier`, `StreamStatusResolver`, `assets.EmoteIndex`, a debug logger, and Twitch Helix-backed channel/game/user/followed-channel lookups; there is no image-related option, resolver, preparer, or renderer, because avatars, badges, emotes, and emoji always render as text. `render.FallbackAssetOptions` carries the `ShowAvatars` flag (set from `avatar_mode != "off"`) that gates whether the avatar chip fragment is included in a rendered row. `internal/storage.AssetCache` still provides a context-aware `GetAsset`/`PutAsset` boundary with an in-memory (`MemoryAssetCache`) and disk-backed (`DiskAssetCache`) implementation, including expired/oldest-first pruning, but the only current caller is `twi doctor`'s asset-cache pruning check — no live rendering path resolves, downloads, or caches image bytes through it. Use `internal/app.FakeChatClient` and `internal/storage.MemoryAssetCache` for deterministic tests.

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
from local config files, secrets, or a Twitch account. Credentialed Twitch chat
and Docker build/runtime checks are
manual or release-specific checks. Replace `origin/main` with the PR base branch
when needed; use plain `git diff --check` for uncommitted local changes.

Release artifact packaging is intentionally separate from the pull-request gate:

```sh
scripts/release-dry-run.sh --out /tmp/twi-release --image twi:local
```

That dry-run builds the supported trimmed binary matrix, emits and verifies
SHA-256 checksums, builds the Docker image, and smokes help, doctor, and mock
chat with temporary config/cache directories and Twitch credential environment
variables cleared. The GitHub release dry-run workflow runs the same script only
on manual dispatch or `v*` tag pushes. See [release.md](release.md) for the
automated versus manual release check split.

Task-specific smoke and metadata checks used by recent iterations:

```sh
go run ./cmd/twi --help
go run ./cmd/twi chat --mock --channel example
go run ./cmd/twi chat --mock --channel one --channel two
go run ./cmd/twi doctor
go run ./cmd/twi config show
go run ./cmd/twi setup --non-interactive --config "$(mktemp -d)/config.toml" --username example --channel example --login-dry-run
go run ./cmd/twi chat --channel example
git diff --check
```

The live chat smoke command is expected to fail safely in environments without
Twitch credentials. It should print redacted guidance for
`TWI_TWITCH_USERNAME`, `TWI_TWITCH_OAUTH_TOKEN`, `--mock`, `chat:read`, and
`chat:edit`, and it should not attempt networking when credentials are absent.

High-throughput chat stress coverage is deterministic and credential-free:

```sh
go test ./internal/animation ./internal/app ./internal/render
go test ./internal/app -run TestLiveShellHighThroughputChatStressHarness
```

The named app stress test feeds a burst of normalized chat, reply, action,
notice, deleted, system, emote, emoji, badge, mention, and Unicode rows through
the live shell model without Twitch credentials, network clients, or wall-clock
sleeps. It asserts fallback rendering at narrow and
normal widths, the bounded reveal queue and overflow count, resize and scroll
behavior, composer input, and send completion state during the burst.

Focused review searches used by the loop:

```sh
rg "go-twitch-irc|helix" internal/app
if rg -n "os\\.Open|http\\.Get|ReadFile|WriteFile" internal/app --glob '!**/*_test.go'; then exit 1; else exit 0; fi
if rg -n "[ \t]+$" PLAN.md .agent-loop/tasks.json .agent-loop/memory.md README.md docs; then exit 1; else exit 0; fi
```

The I/O grep is app-scoped as a guard against `internal/app`'s Bubble Tea
`Update`/`View` methods performing direct file or network I/O; asset fallback
rendering in `internal/render` is pure row construction with no file or
network access of its own.

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
- Helix-compatible adapters for user identity metadata and Twitch emote/badge metadata (emote autocomplete only; avatars, badges, emotes, and emoji always render as text).

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
- Normal messages, mentions, replies, `/me`, notices, deleted messages, badges, emote tokens, emoji glyphs, and partial reveal frames.

Manual verification should include:

- `twi chat --mock`.
- `twi doctor`.
- A low-traffic Twitch channel.
- Sending a test message.
- Terminal resize while connected.
- Reduced/off animation modes.

Record manual evidence in [manual-validation.md](manual-validation.md). If
credentials, a real Twitch channel, or pointer input are unavailable, mark the
check skipped with the environment reason instead of implying it passed.

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
- Async emote-autocomplete metadata lookups.
- Avatars, badges, emotes, and emoji stay text-only (no image rendering path reintroduced).
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

Prefer vertical slices that end in runnable behavior. Keep docs updated when behavior changes, especially around auth, config, text-fallback rendering, and command availability.
