# Product Requirements Matrix

Status: Product source of truth with current MVP status labels.

Source: `PLAN.md`. Current stable Go was verified externally as `go1.26.4` on
2026-07-01; recent agent-loop validation also used `go1.26.4 linux/amd64`.

## Current Behavior Status

| Area | Status | Notes |
| --- | --- | --- |
| Mock chat | Ready | Runs without Twitch credentials or network access. |
| Multi-channel live IRC read/send | Partial | Reads, sends, replies, and sends `/me` actions for configured channels with environment, flat config, or supported Unix saved credentials. Broader manual Twitch evidence remains future work. |
| Diagnostics | Partial | `twi doctor` reports config path, credential presence, Twitch OAuth identity/expiry/scope validation, refresh availability, username mismatch, Twitch IRC reachability, terminal hints, Kitty/Ghostty signals, cache writability/pruning, and feature modes. |
| Login/setup | Partial | `twi setup` writes non-secret config values and can hand off to login. On supported Unix builds, `twi login` runs the browser/local-callback OAuth flow or a `--dry-run` explanation, validates returned tokens, and saves them through the restrictive credential-file fallback without printing them. Windows uses env/config credentials until the selected native Credential Manager backend is implemented. |
| Multi-channel UX | Partial | Per-channel state, local view filters, live routing, keyboard-first sidebar, command palette, optional mouse controls, selected-message inspect, and active live IRC reconnect restart are implemented; real two-channel Twitch manual evidence and transport-specific per-channel reconnect isolation remain pending. |
| Inline terminal images | Partial | Renderer fallback rows, fixed-width prepared cell substitution, cache boundaries, capability diagnostics, visible-row asset event scheduling, and default live resolver/downloader/preparer/renderer wiring exist; manual Kitty/Ghostty validation remains planned for a compatible graphics terminal session. |

## MVP Scope

MVP means the smallest product that proves the chat client can run as a real TUI:

- Twitch chat read and send over IRC for configured channels.
- Credentials from environment variables, config file, or on supported Unix builds the private credential file, with CLI overrides for channel and config path.
- Bubble Tea shell with status bar, chat viewport, composer, compact help, resize handling, and text fallbacks.
- Typed-in reveal for incoming mock and live messages.
- Test fakes for chat transport, send results, config, and animation timing.
- No required terminal image support for MVP; image features must have stable text fallbacks.

Later scope includes image-backed avatars/emotes/emoji in manually validated Kitty/Ghostty-compatible terminals, startup token validation, activity charts, Windows Credential Manager saved credentials, and additional transport/image protocols.

## Support Tiers

| Tier | Target | Required behavior |
| --- | --- | --- |
| Tier 1 | Kitty and Ghostty-compatible terminals with inline images | Full color TUI, mouse where available, Kitty images for avatars/emotes/emoji, stable placeholders, text fallback on asset failure. |
| Tier 2 | Modern true-color Unicode terminals without images | Full color TUI, mouse where available, initials/tokens/Unicode fallback, no layout reflow from missing images. |
| Tier 3 | Basic terminals with reduced visual capability | Readable chat, reduced styling, no required mouse/images, animations can reduce or disable automatically. |

## Matrix

| ID | Area | Requirement | MVP | Later | Testability |
| --- | --- | --- | --- | --- | --- |
| R-001 | Chat read | Connect to Twitch IRC over TLS, authenticate, join configured channels, request tags/commands, and render live `PRIVMSG`, notices, room state, moderation, reconnect, and connection events through normalized messages. | Configured-channel IRC join, core message/notice/status events, fake client tests. Twitch IRC connect/reconnect/disconnect callbacks are connection-level, not independent per-channel events. | EventSub-compatible transport, richer event coverage. | Fake `ChatClient` integration tests; manual low-traffic channel check; reconnect state tests. |
| R-002 | Chat send | Send composer text to the active channel using IRC `Say`; keep send status visible and preserve user text on failure. | Normal sends, selected-message replies, `/me` actions, and failed-send feedback. | Optimistic echo reconciliation and richer rate-limit guidance. | Fake sender success/failure tests; manual send to test channel. |
| R-003 | Avatars | Reserve an avatar column and show a stable fallback when image support or profile data is unavailable. | Initials/color chip fallback plus batched/cached Helix `profile_image_url` metadata and async download/prepare/render wiring for visible live-chat authors when avatar image mode, API credentials, cache, and terminal checks pass. | Manual Kitty/Ghostty validation and tuning. | Golden rows with fallback; fake HTTP/cache/app tests; manual Kitty/Ghostty image check later. |
| R-004 | Twitch emote images | Preserve Twitch emote tokens from IRC tags and render readable fallback tokens. | Parse or preserve emote metadata enough to avoid corrupt text fallback. | Resolve Helix/CDN templates, cache images, render static image placeholders. | Unit tests for emote position parsing and fallback rows; image renderer fake later. |
| R-005 | Emoji images | Preserve Unicode emoji and grapheme clusters in messages. | Native Unicode emoji fallback with grapheme-safe reveal/wrap plus provider metadata, public download/cache, preparation, and renderer wiring for URL-free emoji asset keys. | Manual Kitty/Ghostty validation and provider polish. | Grapheme tests; golden rows with emoji; provider/cache tests; image lookup fake later. |
| R-006 | Typed-in animation | New visible messages reveal quickly without blocking input, scrolling, sending, network receive, or reconnect handling. | Tick-driven reveal over normalized/render fragments for mock and live text fallback rows; modes `off`, `reduced`, `fast`. | Expressive mode, image placeholder transitions, newest-row pulse, high-volume tuning. | Fake clock frame tests; high-throughput degradation tests; manual viewport check. |
| R-007 | Auth | Accept Twitch username and OAuth token securely and require `chat:read`/`chat:edit` for IRC MVP. | Env/config credentials plus Unix saved credentials; actionable missing/invalid credential errors; doctor token validation; no secret printing; `twi login` OAuth validation and restrictive Unix credential-file persistence; setup login handoff; refreshed-token persistence after IRC auth refresh where the credential store is supported; credential storage boundary and fakes. | Auth flags, startup token validation with scope checks, Windows Credential Manager saved credentials, and continued deferral for other non-Unix targets until a backend is selected. | Config/auth/storage validation tests; redaction tests; manual invalid-token check. |
| R-008 | Config | Resolve effective config from CLI overrides > environment > config file > Unix saved credentials > defaults. | Env/config support for username, token, channels, image mode, avatar mode, emoji mode, emote mode, and animation mode; CLI overrides for config path and channels; login reads client ID/secret from env/config; setup writes non-secret config values; Unix saved credential values fill empty credential fields. | Auth/mode flags, explicit config-to-credential migration UX, and Windows Credential Manager saved credential values after the native backend is implemented. | Precedence tests; `config show` redaction tests; setup wizard tests; path tests by platform. |
| R-009 | Multi-channel | Preserve channel-specific history, composer state, connection state, unread counts, and local filters. | Join multiple configured channels; route messages, unread counts, drafts, replies, active sends, local view filters, and scroll state by channel. Local filters can narrow the active view to mentions, broadcaster/mod/VIP messages, notices/system rows, or error-like rows without deleting channel history. Normal/wide terminals show a keyboard-first sidebar, while narrow terminals collapse channel state into the status line. Connection-level IRC states are copied onto configured channel states; manual reconnect restarts the active live IRC transport without clearing per-channel UI state. | Mouse switching polish and isolated per-channel reconnect semantics if a future transport exposes them. | Model tests with two fake channels; manual two-channel Twitch check. |
| R-010 | Mouse | Enable optional mouse interactions without making core workflows mouse-dependent. | `enable_mouse` defaults on and can be disabled through config/env. When enabled, mouse wheel scrolls the chat viewport, sidebar clicks switch channels, composer clicks focus input, and message clicks select reply context. Keyboard equivalents remain available. | Clickable URLs and richer hit regions after security/UX review. | Bubble Tea event tests; manual terminal mouse check. |
| R-011 | Diagnostics | Surface connection, send, auth, terminal capability, and degraded-mode status without destroying chat context. | Status bar/errors in TUI plus `twi doctor` checks for config path, credential presence, Twitch OAuth identity/expiry/scope validation, refresh availability, username mismatch, Twitch reachability, terminal color/mouse/Kitty signals, cache writability/pruning, feature modes, and secret redaction. Selected-message inspect shows redacted normalized and raw tag diagnostics without replacing composer, reply, or scroll state. Opt-in JSON debug logs cover auth, transport, send, asset, render, and connection diagnostics with curated redacted fields. | Startup validation and richer in-app auth diagnostics. | Unit tests for diagnostic output redaction and debug log redaction; manual `doctor` checks. |
| R-012 | Fallbacks | Every optional feature degrades visibly and intentionally: images to initials/tokens/Unicode, animation to reduced/off, API asset failure to cached or text output. | Text fallback rendering, reduced/off animation, no blocking asset paths. | Tiered terminal capability detection, cache-aware degraded states, per-feature mode controls. | Golden fallback snapshots; fake failure tests; manual non-Kitty terminal check. |
| R-013 | Layout and UX | Use a colorful, high-contrast TUI with status bar, viewport, composer, help, and responsive narrow/wide layout. | Root Bubble Tea shell with resize-safe layout, keyboard-first sidebar, command palette, local filter controls, optional mouse controls, and selected-message inspect panel without raw debug-console output. | Activity charts and richer sidebar polish. | Snapshot/golden layout tests at narrow/normal/wide widths; manual resize test. |
| R-014 | Architecture | Keep UI, Twitch transport, rendering, assets, animation, config, and storage behind testable boundaries. | `ChatClient`, normalized message model, config interfaces, animation clock fakes. | EventSub adapter, full asset cache and image renderer interfaces. | Compile-time interface checks; unit tests per package; no UI dependency on Twitch library types. |
| R-015 | Security | Never print secrets in logs, errors, snapshots, config output, or tests. | Redaction helpers and tests before config/auth output ships; structured debug logging is opt-in and redacted by default; credential storage DTO keeps default encoding redacted and requires an explicit file-marshal reveal path; restrictive Unix credential-file implementation rejects unsafe modes and symlinks and uses no-follow opens. | Windows Credential Manager saved credentials only after native backend implementation; other non-Unix secure storage only after explicit platform decisions. | Secret fixture tests; search logs/snapshots for token patterns before handoff. |
| R-016 | Tooling | Use official Go modules and the current stable Go toolchain. | `go 1.26`, `toolchain go1.26.4`, module tools via Go tool directives once module exists. | Periodic latest stable verification before implementation/release. | `go version`, `go mod tidy`, `go fmt ./...`, `go test ./...`, `go vet ./...`, later `govulncheck`. |

## Phase 0 Exit Check

- Requirements matrix exists: this document.
- MVP and later scope are explicit.
- Target terminal support tiers are explicit.
- Current stable Go version is recorded from the provided verification context.
- ADRs exist for IRC transport, Helix asset wrapper, Kitty graphics, normalized message model, animation scheduler, and Go toolchain management.
