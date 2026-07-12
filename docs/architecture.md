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
 terminal rows              internal/storage
```

The app consumes normalized messages and app-facing interfaces. It does not import concrete Twitch IRC callback types, and it does not perform network or disk I/O from `View`.

## Message Flow

Live chat starts in `internal/cli`, which loads config, resolves credentials, validates OAuth when reachable, builds Twitch and asset clients, then hands app-facing options to `internal/app`.

`internal/twitch` converts IRC callbacks into normalized chat messages, notices, moderation events, room state, connection state, and send results. Raw tags are retained for redacted diagnostics, not for main chat rendering.

`internal/app` stores per-channel history, unread counts, composer drafts, reply context, selected-message inspect state, send status, scroll offsets, local filters, and connection state. Channel switching changes the active view without losing per-channel state.

`internal/render` converts normalized messages into width-bounded rows. It handles timestamps, badges, usernames, mentions, replies, action messages, notices, deleted messages, emoji fallbacks, emote-token fallbacks, avatar initials, and image placeholder cells.

## Asset Flow

Asset work is asynchronous and fallback-first:

```text
visible rows -> asset refs -> resolver -> downloader/cache -> preparer -> renderer -> fixed-width cells
     |                                                                            |
     +-------------------------- stable text fallback ----------------------------+
```

The visible chat row is useful before any asset finishes. Resolver, downloader, cache, preparer, and renderer failures keep initials, labels, tokens, or Unicode in place. Permanent failures are recorded by URL-free asset identity so the app avoids repeated failing work without storing private URLs in debug output.

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
- Additional image protocols can implement the image renderer boundary without changing Twitch normalization.
- New asset providers can feed typed asset events without changing chat history state.
- More diagnostics can emit redacted debug fields without exposing raw structs.

Extension work should keep the same rule: the UI depends on internal interfaces and normalized models, not on external service types.

## Theming And Animation

`internal/theme` resolves a `Palette` (background, foreground, accent, muted,
border, surface, warning, error, success) from either a built-in preset name
or a custom hex palette (`config.Config.ResolveTheme`); `internal/app` reads
one active palette (`mockShellModel.theme`) and every widget — including a
full-viewport `Background()` wrap in `View()` — derives its colors from it, so
adding a widget only means reading `m.theme` rather than a new literal.

A single shared `animation.FrameMsg` tick (`internal/animation/clock.go`,
~10fps, skipped when `animation_mode = "off"`) drives every chrome animation
effect — the pulsing status-bar LIVE/REC segment, the channel-switch flash,
the startup splash, and a command-palette typewriter reveal that reuses the
same `Sequence`/`Queue` machinery built for chat-row reveals — instead of each
effect running its own ad hoc ticker. A future animated widget should hook
into this same tick rather than scheduling a second clock.

The real-broadcast LIVE indicator (`internal/twitch.HelixStreamsClient`) and
emote autocomplete (`internal/assets.EmoteIndex`) are wired independently of
the image/asset stack decision in `internal/cli` (`newStreamStatusResolver`,
`newEmoteIndex`) since neither needs a disk cache or terminal image
capability — only `stream_status_mode`/`emote_autocomplete_mode` and Twitch
API credentials. Both are `nil`-able app-facing interfaces so `--mock` mode
and credential-free runs degrade to simulated/sample data instead of failing.
