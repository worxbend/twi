# Architecture

`twi` keeps Twitch transport, authentication, rendering, assets, storage, and Bubble Tea state separated so each lane can be tested without a real Twitch account or a specific terminal.

## System Shape

The runtime shape is a set of narrow boundaries around the Bubble Tea shell:

```text
        config/env/flags              Twitch IRC + Helix
              |                              |
              v                              v
        internal/cli --------------> internal/twitch
              |                              |
              v                              v
        internal/app  <---------- normalized chat events
              |
      +-------+-------------------+
      |                           |
      v                           v
 internal/render            internal/assets
      |                           |
      v                           v
 terminal rows              Twitch asset APIs
```

`internal/storage` is reached from `internal/cli` (credentials) and
`internal/app` (diagnostics), not from `internal/assets`. Verify any edge in
this diagram with `go list -deps ./internal/<pkg>` before relying on it.

The app consumes normalized messages and app-facing interfaces. It does not import concrete Twitch IRC callback types, and it does not perform network or disk I/O from `View`.

## Message Flow

Live chat starts in `internal/cli`, which loads config, resolves credentials, validates OAuth when reachable, builds Twitch and asset clients, then hands app-facing options to `internal/app`.

`internal/twitch` converts IRC callbacks into normalized chat messages, notices, moderation events, room state, connection state, and send results. Raw tags are retained for redacted diagnostics, not for main chat rendering.

`internal/app` stores per-channel history, unread counts, composer drafts, reply context, selected-message inspect state, send status, scroll offsets, local filters, and connection state. Channel switching changes the active view without losing per-channel state. The open channel set is dynamic: the `/channels` picker adds and the sidebar removes channels at runtime, joining and parting through the optional `ChannelJoiner` transport capability rather than reconnecting, and an empty set is a normal state rather than an error.

`internal/render` converts normalized messages into width-bounded rows. It handles timestamps, badges, usernames, mentions, replies, action messages, notices, deleted messages, emoji fallbacks, emote-token fallbacks, avatar initials. Username color is a deterministic, case-normalized hash of the Twitch login (falling back to display name or author ID), selected for readable contrast against both message surfaces; the same author therefore keeps the same distinct color without UI-owned mutable color state.

## Asset metadata flow

Helix global/channel emote metadata feeds asynchronous autocomplete through
`internal/assets.EmoteIndex`. Message badges and emotes already carry normalized
IRC/EventSub metadata. Rows render text, initials, badge labels, emote tokens,
and native Unicode. There is no image downloader/cache/preparer or terminal
image protocol in the shipped rendering path.

## Credential Flow

Credential precedence is:

```text
CLI channel/config flags > environment > flat config > Unix saved credentials > defaults
```

Saved credentials are Unix-only. The restrictive credential file uses a private platform config directory, exact `0700` directory permissions, exact `0600` file permissions, symlink rejection, no-follow opens, and atomic replacement. Non-Unix saved credentials are out of scope and must keep returning a redacted unsupported-platform error.

## Debug Flow

Debug logs are JSON lines written only when explicitly enabled. Callers send curated fields through `internal/debuglog`; auth, config, storage, transport, asset, and render code must not dump raw private data. Debug-log files are opened through hardened helpers that reject unsafe files before writing.

## Extension Points

The project can grow without collapsing boundaries:

- A future transport can implement the app-facing chat client without changing the renderer.
- New metadata providers can extend autocomplete behind typed interfaces.
- More diagnostics can emit redacted debug fields without exposing raw structs.

Extension work should keep the same rule: the UI depends on internal interfaces and normalized models, not on external service types.

## Theming And Animation

`internal/theme` resolves a `Palette` (background, foreground, accent, muted,
border, surface, warning, error, success) from either a built-in preset name
or a custom hex palette (`config.Config.ResolveTheme`); `internal/app` reads
one active palette (`mockShellModel.theme`) and every widget derives its colors
from it. The full viewport and terminal OSC background use a slightly darker
derived canvas, while framed panes share a raised surface, icon-bearing title,
quiet frame, and role-colored left rail. Adding a widget therefore means reading
`m.theme` and using the shared pane primitive rather than adding new literals.

A single shared `animation.FrameMsg` tick (`internal/animation/clock.go`,
~10fps, skipped when `animation_mode = "off"`) drives every chrome animation
effect — the theme-derived top-bar, auxiliary focused pane-title/rail, and
splash gradients, pulsing status-bar LIVE/REC and incoming-message rails, the staged
block-logo splash, and a command-palette typewriter reveal that reuses the same
`Sequence`/`Queue` machinery built for chat-row reveals — instead of each
effect running its own ad hoc ticker. The Chat pane frame and title are kept
static even while its message content reveals.

Chrome labels reach that clock through `animation.TextFrame`
(`internal/animation/text.go`), which renders a label under one of four
effects — `typewriter`, `gradient-wave`, `shimmer`, `bounce` — as a pure
function of elapsed time. It returns styled cells rather than escape
sequences, keeping the package free of terminal I/O; `internal/app`
(`animated_text.go`) paints those cells over the surface background and pads
them. Every effect preserves its label's display width on every frame, so an
animated label cannot reflow the pane around it, and `animation_mode = "off"`
collapses each one to its static frame with the wording and layout unchanged.
The splash tagline and strapline and the no-channels-open empty state are
drawn this way. Chat message text stays on `Sequence`, which keeps chat
readable and scroll-stable. After local filtering, adjacent
messages are grouped by normalized Twitch login (then stable author ID/display
name fallbacks); authorless events receive separate message/event-identity
groups instead of being conflated by event type. Consecutive messages in one author group share the same
background surface and colored rail; an explicit non-selectable separator row
is inserted only when the visible author changes. These decorations and their
scroll/mouse row accounting are added at the app layout boundary so the
normalized message renderer stays reusable.
Moving gradients use a mirrored `start → end → start` palette whose first
and last colors match, avoiding a hard seam when phase rotation wraps at either
side of a line or rail.
A borderless, inset composer follows the same boundary: it renders a
theme-surface panel with a focus rail, shared-clock block cursor, optional reply
context, and a compact channel/send-state footer without changing the existing
per-channel draft or send-queue model. It collapses to a three-row or plain
text form when terminal dimensions cannot support the full four-row surface.
A future animated widget should hook into this same tick rather than
scheduling a second clock.

The real-broadcast LIVE indicator (`internal/twitch.HelixStreamsClient`) and
emote autocomplete (`internal/assets.EmoteIndex`) are wired independently of
rendering in `internal/cli` (`newStreamStatusResolver`,
`newEmoteIndex`) since neither needs a disk cache or terminal image
capability — only `stream_status_mode`/`emote_autocomplete_mode` and Twitch
API credentials. Both are `nil`-able app-facing interfaces so `--mock` mode
and credential-free runs degrade to simulated/sample data instead of failing.

## Automation and durable history

Producer, consumer, foreground recorder, and local history commands are implemented
through UI-independent services and explicit public DTOs. See [Automation](automation.md) for
capabilities, examples, coverage/cursor guarantees, retention, and validation limits.
Live Twitch observation parity requires controlled credentialed validation.

### Headless runtime ownership

`internal/chatservice` owns live readiness, bounded ingestion, reconnect gaps,
and periodic auth checks. Its reader port requires Connect/Close only; EventSub
does not pretend to send. `internal/runtimeauth` validates capability scopes and
coordinates token rotation using private account locks. `internal/sendquota`
reserves global/account and canonical channel permits before producer/TUI sends.
`internal/history` owns private SQLite WAL transactions, deduplication, retention,
moderation redaction, coverage, and opaque cursors. `internal/automation` owns
versioned public DTOs; `internal/output` encodes safe JSONL/text records. CLI
composition supplies cancellation and streams without starting terminal state
for headless commands. Existing TUI lifecycle facades preserve compatibility.

IRC socket lifetime is managed by twi; the maintained go-twitch-irc parser still
normalizes wire messages. Hidden library reconnects are not allowed to bypass
authentication permits. Control writes and joins use separate bounded queues so
rate waits preserve PING/PONG handling. IRC/EventSub reserve shared per-account
channel leases before subscribing and release them after socket shutdown.
