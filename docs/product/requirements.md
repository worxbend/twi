# Product Requirements Matrix

Status: Product source of truth with current MVP status labels.

Source: `PLAN.md`. Current stable Go was verified externally as `go1.26.4` on
2026-07-01; recent agent-loop validation also used `go1.26.4 linux/amd64`.

## Current Behavior Status

| Area | Status | Notes |
| --- | --- | --- |
| Mock chat | Ready | Runs without Twitch credentials or network access. |
| One-channel live IRC read/send | Partial | Reads, sends, replies, and sends `/me` actions for one configured channel with env/config credentials. Token identity/scope validation and broader manual Twitch evidence remain future work. |
| Diagnostics | Partial | `twi doctor` reports config path, credential presence, unverified scope hints, Twitch IRC reachability, terminal hints, Kitty/Ghostty signals, cache writability, and feature modes. Helix-backed token validation is planned. |
| Login/setup | Planned | `twi login` is not implemented. |
| Multi-channel live chat | Planned | Current live mode intentionally supports one channel. |
| Inline terminal images | Planned | Current renderer uses text, initials, Unicode, badge, and emote-token fallbacks only. |

## MVP Scope

MVP means the smallest product that proves the chat client can run as a real TUI:

- One-channel Twitch chat read and send over IRC.
- Credentials from environment variables or config file, with CLI overrides for channel and config path.
- Bubble Tea shell with status bar, chat viewport, composer, compact help, resize handling, and text fallbacks.
- Typed-in reveal for incoming mock and live messages.
- Test fakes for chat transport, send results, config, and animation timing.
- No required terminal image support for MVP; image features must have stable text fallbacks.

Later scope includes multi-channel UX, image-backed avatars/emotes/emoji, Helix-backed token validation, interactive login, setup wizard, command palette, activity charts, and additional transport/image protocols.

## Support Tiers

| Tier | Target | Required behavior |
| --- | --- | --- |
| Tier 1 | Kitty and Ghostty-compatible terminals with inline images | Full color TUI, mouse where available, Kitty images for avatars/emotes/emoji, stable placeholders, text fallback on asset failure. |
| Tier 2 | Modern true-color Unicode terminals without images | Full color TUI, mouse where available, initials/tokens/Unicode fallback, no layout reflow from missing images. |
| Tier 3 | Basic terminals with reduced visual capability | Readable chat, reduced styling, no required mouse/images, animations can reduce or disable automatically. |

## Matrix

| ID | Area | Requirement | MVP | Later | Testability |
| --- | --- | --- | --- | --- | --- |
| R-001 | Chat read | Connect to Twitch IRC over TLS, authenticate, join one channel, request tags/commands, and render live `PRIVMSG`, notices, room state, moderation, reconnect, and connection events through normalized messages. | One channel, core message/notice/status events, fake client tests. | EventSub-compatible transport, richer event coverage. | Fake `ChatClient` integration tests; manual low-traffic channel check; reconnect state tests. |
| R-002 | Chat send | Send composer text to the active channel using IRC `Say`; keep send status visible and preserve user text on failure. | Normal sends, selected-message replies, `/me` actions, and failed-send feedback. | Optimistic echo reconciliation and richer rate-limit guidance. | Fake sender success/failure tests; manual send to test channel. |
| R-003 | Avatars | Reserve an avatar column and show a stable fallback when image support or profile data is unavailable. | Initials/color chip fallback only. | Batch Helix Get Users, cache `profile_image_url`, download/cache/crop images, render through Kitty protocol. | Golden rows with fallback; fake asset cache; manual Kitty/Ghostty image check later. |
| R-004 | Twitch emote images | Preserve Twitch emote tokens from IRC tags and render readable fallback tokens. | Parse or preserve emote metadata enough to avoid corrupt text fallback. | Resolve Helix/CDN templates, cache images, render static image placeholders. | Unit tests for emote position parsing and fallback rows; image renderer fake later. |
| R-005 | Emoji images | Preserve Unicode emoji and grapheme clusters in messages. | Native Unicode emoji fallback with grapheme-safe reveal/wrap. | Map emoji graphemes to local/provider image assets and render through image pipeline. | Grapheme tests; golden rows with emoji; image lookup fake later. |
| R-006 | Typed-in animation | New visible messages reveal quickly without blocking input, scrolling, sending, network receive, or reconnect handling. | Tick-driven reveal over normalized/render fragments for mock and live text fallback rows; modes `off`, `reduced`, `fast`. | Expressive mode, image placeholder transitions, newest-row pulse, high-volume tuning. | Fake clock frame tests; high-throughput degradation tests; manual viewport check. |
| R-007 | Auth | Accept Twitch username and OAuth token securely and require `chat:read`/`chat:edit` for IRC MVP. | Env/config credentials; actionable missing/invalid credential errors; no secret printing. | Interactive login, auth flags, refresh/secure storage, Helix token validation with scope checks. | Config/auth validation tests; redaction tests; manual invalid-token check. |
| R-008 | Config | Resolve effective config from CLI overrides > environment > config file > defaults. | Env/config support for username, token, channels, image mode, avatar mode, emoji mode, emote mode, and animation mode; CLI overrides for config path and channels. | `twi login`, setup wizard, auth/mode flags, config migration, OS keychain. | Precedence tests; `config show` redaction tests; path tests by platform. |
| R-009 | Multi-channel | Preserve channel-specific history, composer state, connection state, and unread counts. | Not required beyond internal model shape not blocking future multi-channel. | Join multiple channels, sidebar, keyboard/mouse switching, isolated reconnects. | Model tests with two fake channels later; manual switch test. |
| R-010 | Mouse | Enable optional mouse interactions without making core workflows mouse-dependent. | Keyboard-first UI; mouse feature flag can exist but behavior may be minimal. | Scroll viewport, click channel, click composer/message, bubblezone hit regions. | Bubble Tea event tests; manual terminal mouse check. |
| R-011 | Diagnostics | Surface connection, send, auth, terminal capability, and degraded-mode status without destroying chat context. | Status bar/errors in TUI plus `twi doctor` checks for config path, credential presence, unverified required scopes, Twitch reachability, terminal color/mouse/Kitty signals, cache writability, feature modes, and secret redaction. | Helix-backed token identity, expiry, and scope validation. | Unit tests for diagnostic output redaction; manual `doctor` checks. |
| R-012 | Fallbacks | Every optional feature degrades visibly and intentionally: images to initials/tokens/Unicode, animation to reduced/off, API asset failure to cached or text output. | Text fallback rendering, reduced/off animation, no blocking asset paths. | Tiered terminal capability detection, cache-aware degraded states, per-feature mode controls. | Golden fallback snapshots; fake failure tests; manual non-Kitty terminal check. |
| R-013 | Layout and UX | Use a colorful, high-contrast TUI with status bar, viewport, composer, help, and responsive narrow/wide layout. | Root Bubble Tea shell with resize-safe layout and no raw debug-console output. | Sidebar, inspect panel, command palette, activity charts. | Snapshot/golden layout tests at narrow/normal/wide widths; manual resize test. |
| R-014 | Architecture | Keep UI, Twitch transport, rendering, assets, animation, config, and storage behind testable boundaries. | `ChatClient`, normalized message model, config interfaces, animation clock fakes. | EventSub adapter, full asset cache and image renderer interfaces. | Compile-time interface checks; unit tests per package; no UI dependency on Twitch library types. |
| R-015 | Security | Never print secrets in logs, errors, snapshots, config output, or tests. | Redaction helpers and tests before config/auth output ships. | Restrictive config permissions or OS keychain, structured debug logging. | Secret fixture tests; search logs/snapshots for token patterns before handoff. |
| R-016 | Tooling | Use official Go modules and the current stable Go toolchain. | `go 1.26`, `toolchain go1.26.4`, module tools via Go tool directives once module exists. | Periodic latest stable verification before implementation/release. | `go version`, `go mod tidy`, `go fmt ./...`, `go test ./...`, `go vet ./...`, later `govulncheck`. |

## Phase 0 Exit Check

- Requirements matrix exists: this document.
- MVP and later scope are explicit.
- Target terminal support tiers are explicit.
- Current stable Go version is recorded from the provided verification context.
- ADRs exist for IRC transport, Helix asset wrapper, Kitty graphics, normalized message model, animation scheduler, and Go toolchain management.
