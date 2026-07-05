# Code Style

This guide describes the local engineering style for `twi`. It is intentionally specific to this repository: Go first, small packages, explicit errors, strong redaction, deterministic tests, and terminal UI code that never blocks rendering.

## Go Baseline

- Use the Go version and `toolchain` directive already pinned in [../go.mod](../go.mod).
- Use Go modules only.
- Prefer the standard library and existing dependencies before adding anything new.
- Keep `go.mod` and `go.sum` machine-managed through `go get`, `go mod tidy`, and `go mod edit`.
- Use `go tool govulncheck` and `go tool staticcheck` from the module `tool` directives.

## Package Boundaries

| Package | Owns | Must not own |
| --- | --- | --- |
| `cmd/twi` | Process entrypoint and CLI handoff. | Business logic. |
| `internal/cli` | Command parsing, config wiring, debug-log setup, startup orchestration. | Bubble Tea view behavior or Twitch protocol parsing. |
| `internal/config` | Flat config, env mapping, defaults, display redaction. | Credential persistence or network clients. |
| `internal/app` | Bubble Tea model, update loop, view composition, fake/live chat boundary, per-channel UI state. | Concrete Twitch IRC types or blocking I/O in `View`. |
| `internal/twitch` | IRC transport, OAuth validation, Helix identity/assets adapters, normalized Twitch events. | Bubble Tea components. |
| `internal/render` | Message fragments, wrapping, image placeholders, terminal image preparation/rendering. | Network downloads or Twitch client ownership. |
| `internal/assets` | Asset metadata resolution, public image downloading, cache write-through, safe asset events. | UI state mutation. |
| `internal/storage` | Disk cache, filesystem probes, Unix credential-file persistence, storage test fakes. | UI decisions or non-Unix credential backends. |
| `internal/animation` | Grapheme-safe reveal units and bounded queues. | Raw IRC parsing or terminal I/O. |
| `internal/debuglog` | Redacted JSON-line debug records. | Raw struct dumping. |
| `internal/theme` | Palette and contrast correction. | App state. |

## Error Handling

Return explicit errors with enough context for operators to act, but never include token values, refresh tokens, client secrets, callback codes, OAuth state, authorization URLs, bearer headers, credential file contents, or raw private config values.

Use `errors.Is`/`errors.As` for sentinel behavior such as unsupported credential persistence. Wrap errors at package boundaries when the caller needs action-oriented context. Keep transient network failures distinct from definitive invalid-token states.

## Context And Cancellation

Every network, cache, asset, login, and transport operation should accept `context.Context` or be called from a function that already owns cancellation. Reconnect must close/cancel the old transport before replacing it. Asset downloads and image preparation should stop promptly when the UI no longer needs the work.

## Terminal UI Rules

Bubble Tea `Update` can schedule commands, mutate model state, and consume typed messages. Bubble Tea `View` must stay pure: no network calls, no filesystem writes, no blocking reads, and no sleeps. If a view needs data, schedule work through a command and render a stable fallback until the result arrives.

Keep narrow layouts usable. Sidebar, help, status, composer, inspect panel, and chat rows must degrade predictably when terminal width or height is small.

## Rendering Rules

Normalize Twitch events before rendering. Render fragments, not raw IRC strings. Use width-aware APIs for terminal layout. Do not slice user-visible strings by byte or rune when grapheme clusters, emoji, combining marks, ANSI styles, or image placeholders can be involved.

Image placeholders must reserve stable width so late avatar, emote, badge, or emoji assets do not reflow chat history. Text, initials, Unicode emoji, compact badge labels, and emote tokens must remain good fallbacks.

## Secret And Debug Rules

Debug logging is opt-in. Debug records must use curated fields and redaction helpers. Do not log raw `ConnectionState`, `ChatMessage`, transport events, raw tag maps, source URLs, HTTP headers, OAuth callback queries, or unfiltered errors from auth/storage/network code.

Tests should use fake secret markers that are obvious and easy to scan for. Do not place real credentials in fixtures.

## Comment Style

Write comments where they preserve design intent, security constraints, package boundaries, or non-obvious invariants. Avoid comments that narrate simple assignments or repeat function names.

Package comments should explain the package responsibility and its boundary. Exported identifiers need comments when the name alone does not explain safe use, redaction behavior, concurrency expectations, or platform limitations.

## Test Style

Prefer focused table tests and deterministic fakes. Do not use wall-clock sleeps when a fake clock or explicit message can drive the behavior. For high-throughput chat and animation, assert queue bounds, overflow behavior, stable layout, and input responsiveness.

Use real files only when filesystem permissions, symlink rejection, cache paths, or atomic replacement behavior are under test. Keep test temp directories isolated and credential-free.

## Documentation Style

Docs should distinguish ready, partial, planned, manual, credentialed, and out-of-scope behavior. Link related docs with relative `.md` paths. Avoid support claims that require evidence unless [manual-validation.md](manual-validation.md) records that evidence.
