# Initial Agent Backlog

Status: Historical backlog plus current post-MVP planning notes. Current task
status is tracked in `.agent-loop/tasks.json`.

Progress as of the initial swarm pass:

- Done: Phase 0 requirements matrix, risk register, backlog, and six ADRs.
- Done: Go module bootstrap, CLI shell, config precedence/redaction tests, normalized message model skeleton, Bubble Tea mock chat shell, module tool directives for `govulncheck`/`staticcheck`, Twitch IRC read adapter, the active-channel composer send queue, selected-message replies, `/me` action sends, and per-channel live routing.
- Current status labels: mock chat is ready; multi-channel live IRC read/send,
  keyboard-first channel sidebar, and diagnostics are partial; `twi login`,
  richer multi-channel UI beyond the sidebar, and
  inline terminal images are planned.
- Credential rule: Twitch username/token values currently come from
  environment variables or the flat config file; CLI overrides cover channel
  and config path.
- Remaining near-term work: current documentation alignment, richer live-chat
  credential validation, high-throughput hardening, and real asset/image
  rendering.

Each task is intended to fit one implementation loop. Agents should keep write scope to
the listed files where possible and use fakes before network-dependent code.

## Task 1

Task: Create ADR skeletons for Phase 0 architecture decisions.
Owner lane: Product architect.
Goal: Record the decisions that implementation agents should not re-litigate.
Context: `PLAN.md` requires ADRs for Twitch IRC MVP transport, Helix wrapper, Kitty graphics, normalized messages, animation scheduler, and Go toolchain management.
Files likely touched: `docs/adr/*`.
Implementation notes: Keep each ADR short: status, context, decision, consequences, verification. Use `PLAN.md` and `docs/product/requirements.md` as source.
Acceptance criteria: ADR skeletons exist for all six decisions and link back to the relevant product requirement IDs.
Verification: Read ADRs for consistency; `rg "Status:|Decision:" docs/adr`.
Risks: ADR path is outside the current Phase 0 write scope and needs explicit ownership.
Follow-ups: Fill in ADR consequences after Phase 1 interface design.

## Task 2

Task: Initialize Go module and CLI shell.
Owner lane: QA/release engineer.
Goal: Create a buildable project with a `twi` entrypoint.
Context: The repo currently needs Go module setup before implementation work can compile.
Files likely touched: `go.mod`, `go.sum`, `cmd/twi/main.go`.
Implementation notes: Use official Go tooling; set module name intentionally; avoid hand-editing dependency versions.
Acceptance criteria: `go test ./...` passes and `go run ./cmd/twi --help` prints basic help.
Verification: `go version`; `go mod tidy`; `go test ./...`; `go run ./cmd/twi --help`.
Risks: Local Go is `go1.26.0`, while verified stable is `go1.26.4`; rely on `GOTOOLCHAIN=auto`.
Follow-ups: Add pinned module tools.

## Task 3

Task: Pin Go toolchain and module tools.
Owner lane: QA/release engineer.
Goal: Make builds reproducible across agents.
Context: Phase 0 records `go1.26.4` as current stable on 2026-07-01.
Files likely touched: `go.mod`, `go.sum`.
Implementation notes: Set `go 1.26`, `toolchain go1.26.4`, and add Go `tool` directives for `govulncheck` and possibly `staticcheck`.
Acceptance criteria: Module records the stable toolchain and tools without unexpected dependency drift.
Verification: `go env GOTOOLCHAIN`; `go mod tidy`; `go test ./...`; `go tool govulncheck ./...` once added.
Risks: Tool support may lag the latest Go patch.
Follow-ups: Add CI gate for tooling commands.

## Task 4

Task: Implement config precedence and secret redaction.
Owner lane: Core TUI engineer.
Goal: Load credentials and options safely from flags, env, and config.
Context: MVP requires credentials and modes from env/config plus CLI channel/config-path overrides with no secret leakage.
Files likely touched: `internal/config`, `cmd/twi`.
Implementation notes: Implement precedence flags > env > file > defaults. Add redaction before any config output or error formatting.
Acceptance criteria: Username, token, channels, image modes, and animation mode resolve predictably; token is never printed.
Verification: Unit tests for precedence and redaction; `go test ./internal/config`.
Risks: Early CLI shape may change; keep config package independent.
Follow-ups: Add `twi config show` and `twi config path`.

## Task 5

Task: Build mock Bubble Tea shell.
Owner lane: Core TUI engineer.
Goal: Provide a runnable non-network TUI with the core layout.
Context: Phase 2 requires a status bar, viewport, composer textarea, compact help, resize handling, and theme.
Files likely touched: `internal/app`, `internal/theme`, `cmd/twi`.
Implementation notes: Use a single root model; keep update/view non-blocking; support narrow layout by hiding nonessential regions.
Acceptance criteria: `twi chat --mock` opens a TUI with status, chat viewport, composer, help, and coherent resize behavior.
Verification: `go test ./internal/app`; manual `go run ./cmd/twi chat --mock`.
Risks: Visual scope creep; keep this to shell and mock data.
Follow-ups: Add layout golden snapshots.

## Task 6

Task: Add deterministic typed-in animation for mock messages. Status: implemented in the current mock shell.
Owner lane: Motion engineer.
Goal: Prove incoming rows can reveal without blocking the TUI.
Context: Animation is core product behavior and must be tick-driven.
Files likely touched: `internal/animation`, `internal/app`, `internal/render`.
Implementation notes: Use an injectable clock/ticker. Reveal render fragments or grapheme units, not raw bytes.
Acceptance criteria: Mock messages reveal in `off`, `reduced`, and `fast` modes; input and scrolling remain responsive.
Verification: Fake clock tests; `go test ./internal/animation ./internal/app`.
Risks: Early renderer may be simple; preserve an interface for richer fragments.
Follow-ups: Add high-throughput degradation.

## Task 7

Task: Define `ChatClient` and normalized message model.
Owner lane: Twitch integration engineer.
Goal: Decouple UI/rendering from Twitch IRC library types.
Context: Architecture requires a Twitch transport boundary and normalized messages before real IRC work.
Files likely touched: `internal/twitch`, `internal/render`, `internal/app`.
Implementation notes: Include IDs, channel, timestamp, author fields, badges, fragments, emotes, reply metadata, type, moderation state, and raw tags for debug only.
Acceptance criteria: App can consume fake normalized messages through an interface; no Twitch library imports in `internal/app`.
Verification: Compile-time interface check; fake stream integration test; `rg "go-twitch-irc|helix" internal/app` returns no app coupling.
Risks: Over-modeling; include required fields but allow optional metadata.
Follow-ups: Add IRC adapter conversion.

## Task 8

Task: Implement Twitch IRC read path behind `ChatClient`.
Owner lane: Twitch integration engineer.
Goal: Receive real Twitch chat messages for one channel.
Context: MVP transport is Twitch IRC using `go-twitch-irc/v4`.
Files likely touched: `internal/twitch`, `internal/app`, `cmd/twi`.
Implementation notes: Connect over TLS through the library, request tags/commands, join one channel, bridge callbacks into typed Bubble Tea messages.
Acceptance criteria: Real channel messages appear live; connection and reconnect states are visible; invalid credentials produce actionable errors.
Verification: Unit tests with fake callbacks; manual low-traffic channel check; `go test ./internal/twitch ./internal/app`.
Risks: Requires Twitch credentials for manual verification.
Follow-ups: Add room state, notice, moderation, and reconnect edge cases.

## Task 9

Task: Implement composer send path and send queue. Status: implemented for active-channel normal sends, selected-message replies, and `/me` action sends.
Owner lane: Twitch integration engineer.
Goal: Send messages from the TUI without losing user text on failure.
Context: MVP needs IRC `Say` for the active channel and visible send status.
Files likely touched: `internal/app`, `internal/twitch`.
Implementation notes: Queue sends through commands; clear composer only after chosen queued/accepted state; expose rate-limit feedback.
Acceptance criteria: User can send a message to the active channel; failures show a reason and preserve text.
Verification: Fake sender success/failure tests; manual send to test channel; `go test ./internal/app ./internal/twitch`.
Risks: Twitch send restrictions may vary by account/channel.
Follow-ups: Validate live reply/action behavior with user-owned Twitch credentials.

## Task 10

Task: Implement render fragments, wrapping, and fallback styling.
Owner lane: Rendering engineer.
Goal: Render readable chat rows with stable fallback behavior.
Context: Rich rendering depends on fragments for text, mentions, badges, emoji, emotes, avatars, replies, and moderation state.
Files likely touched: `internal/render`, `internal/theme`, `internal/app`.
Implementation notes: Use width-aware layout, contrast-correct username colors, stable image placeholders, and golden snapshots.
Acceptance criteria: Normal, mention, reply, badge, emoji, emote-token, notice, deleted, and partially revealed rows render cleanly at multiple widths.
Verification: Golden tests for narrow/normal/wide rows; `go test ./internal/render`.
Risks: Unicode width mismatches across terminals.
Follow-ups: Add image-backed asset rendering.

## Task 11

Task: Add asset pipeline interfaces and text fallback tests.
Owner lane: Asset/image engineer.
Goal: Prepare avatars, emotes, emoji, and badges without blocking MVP.
Context: Image rendering is core later scope, but MVP must define fallback contracts early.
Files likely touched: `internal/assets`, `internal/storage`, `internal/render`.
Implementation notes: Completed in T015 with renderer placeholder widths, context-aware storage cache contracts, and no-image fallback snapshots. Real downloads and terminal image drawing remain future work. Do not download assets in `View`.
Acceptance criteria: Renderer can request asset placeholders and render fallbacks when assets are missing, disabled, or unsupported.
Verification: Fake cache/renderer tests; fallback golden snapshots; code search for I/O in render/view paths.
Risks: Designing too much before real assets; keep interfaces minimal.
Follow-ups: Add Helix avatar lookup and Kitty renderer.

## Task 12

Task: Add first `twi doctor` diagnostics skeleton. Status: implemented in T016.
Owner lane: QA/release engineer.
Goal: Give users and agents a single command for setup visibility.
Context: OAuth token validation is now wired into `twi doctor`; diagnostics reduce support ambiguity without requiring login/setup.
Files likely touched: `internal/app`, `internal/config`, `internal/cli`.
Implementation notes: Completed in T016 with config path state, credential presence without values, token identity/expiry/scope validation, Twitch IRC reachability, terminal env hints, Kitty/Ghostty signals, cache writability, feature modes, and redacted output.
Acceptance criteria: `twi doctor` runs without Twitch credentials and never prints secrets.
Verification: Unit tests for diagnostic redaction; manual `go run ./cmd/twi doctor`.
Risks: Terminal feature detection may be incomplete initially.
Follow-ups: Add startup token validation and interactive auth recovery.

## Expanded Implementation Plan

Status: Updated after the initial MVP slices. The current app already has a
Bubble Tea chat shell, deterministic mock mode, multi-channel Twitch IRC
read/send routing, replies, `/me` actions, typed reveal animation, config
precedence/redaction, diagnostics, normalized Twitch events, and text-only
asset fallbacks. The remaining plan below focuses on turning planned
extension points into runnable vertical slices.

### Phase 1: MVP Hardening And Reality Checks

Task: Reconcile documentation and runtime behavior.
Owner lane: QA/release engineer.
Goal: Make README, quickstart, config, auth, Docker, terminal image docs, and
the product requirements describe the same shipped behavior.
Context: Several docs mention planned work that is now partially implemented,
while image and login features remain planned.
Files likely touched: `README.md`, `docs/quickstart.md`, `docs/auth.md`,
`docs/config.md`, `docs/development.md`, `docs/terminal-images.md`,
`docs/product/requirements.md`.
Implementation notes: Prefer explicit status labels: ready, partial, planned.
Do not document secrets or credential examples that look real.
Acceptance criteria: Users can identify the supported commands, required
credentials, config precedence, Docker modes, and image limitations without
reading source.
Verification: `go run ./cmd/twi --help`; `go run ./cmd/twi config show`;
`go run ./cmd/twi doctor`; docs link/read-through check.
Risks: Documentation can drift again unless each feature slice updates docs.
Follow-ups: Add release checklist entries for docs parity.

Task: Validate live IRC behavior against Twitch with manual evidence.
Owner lane: Twitch integration engineer.
Goal: Confirm read, send, reply, `/me`, reconnect status, and credential
failure paths against a real low-traffic channel.
Context: Unit tests cover fakes and callback normalization, but Twitch
behavior still needs a guarded manual check.
Files likely touched: `docs/auth.md`, `docs/development.md`,
`docs/product/risk-register.md`, tests only if issues are found.
Implementation notes: Use a test account/channel. Never record or commit
OAuth tokens, client secrets, logs, terminal captures, or screenshots
containing credentials.
Acceptance criteria: Manual notes identify verified flows and any Twitch-side
limitations; failures produce actionable redacted errors.
Verification: `go test ./internal/twitch ./internal/app`; manual
`twi chat --channel <test-channel>` with read/send/reply/action checks.
Risks: Requires valid credentials and network access; send scopes or channel
permissions may vary by account.
Follow-ups: Convert any reproducible edge case into a fake-client or transport
unit test.

Task: Improve credential and token diagnostics before interactive login.
Owner lane: Auth/platform engineer.
Goal: Validate token identity, expiry, and scopes through Twitch OAuth/Helix
without exposing secrets.
Context: `twi doctor` reports credential presence and validates token identity,
expiry, scopes, username ownership, and refresh availability; IRC auth refresh
is in-memory only.
Files likely touched: `internal/twitch`, `internal/app/doctor.go`,
`internal/config`, `docs/auth.md`, `docs/config.md`.
Implementation notes: Added a small Twitch identity/validation client behind an
interface and wired it into `twi doctor`. Check `chat:read` and `chat:edit`;
report missing scope, expired token, username mismatch, and refresh
availability. Keep all error messages credential-safe.
Acceptance criteria: `twi doctor` distinguishes missing, malformed, expired,
wrong-user, and missing-scope credentials; tests prove redaction.
Verification: HTTP fake tests; targeted tests for `internal/twitch`,
`internal/app`, and `internal/config`; manual invalid-token doctor check.
Risks: Twitch API response shapes and rate limits can change; keep the adapter
thin and test with captured shape fixtures that contain no secrets.
Follow-ups: Reuse the validation client for `twi login`.

Task: Add high-throughput animation and rendering degradation tests.
Owner lane: Motion/rendering engineer.
Goal: Keep the chat responsive when busy channels produce more messages than
can be animated.
Context: The reveal queue is bounded, but stress behavior should be explicit.
Files likely touched: `internal/animation`, `internal/app`,
`internal/render`, `docs/development.md`.
Implementation notes: Prefer deterministic fake clocks and fake chat bursts.
Complete or skip older off-screen reveals when the queue is full.
Acceptance criteria: Input, scrolling, resize, and send state remain responsive
under burst loads; queue bounds are asserted.
Verification: `go test ./internal/animation ./internal/app ./internal/render`;
targeted race test if timing code changes.
Risks: Snapshot tests can become brittle if they assert decorative styling.
Follow-ups: Add a local stress/smoke command only if unit tests are not enough.

### Phase 2: Asset Metadata And Cache Pipeline

Task: Create `internal/assets` service boundaries.
Owner lane: Asset/image engineer.
Goal: Define asynchronous asset resolution for avatars, emotes, emoji, and
badges without coupling render or Bubble Tea views to network/file I/O.
Context: `internal/storage.AssetCache` and `render.ImageRenderer` exist, but
no resolver package fills assets yet.
Files likely touched: `internal/assets`, `internal/storage`,
`internal/render`, `internal/app`, `docs/development.md`.
Implementation notes: Use small interfaces for identity lookup, metadata
lookup, download, cache, and app-facing asset events. Render paths should
consume already-known fallback or image cells only.
Acceptance criteria: Fake resolver tests can simulate cache hit, cache miss,
download failure, and cancellation; app views do not perform blocking I/O.
Verification: Targeted tests for `internal/assets`, `internal/storage`,
`internal/render`, and `internal/app`; search `internal/app` and
`internal/render` for blocking file or network calls.
Risks: Overdesign before real assets; keep the first service minimal and
driven by avatar/emote needs.
Follow-ups: Add persistent disk cache.

Task: Implement persistent bounded asset cache.
Owner lane: Storage engineer.
Goal: Store image files and metadata under the user cache directory with TTL,
size bounds, and context-aware operations.
Context: Current cache storage is in-memory and test-only.
Files likely touched: `internal/storage`, `internal/config`,
`docs/terminal-images.md`, `docs/config.md`.
Implementation notes: Use deterministic paths by asset kind and ID. Store
metadata separately from bytes. Never store OAuth tokens, client secrets, or
credential-bearing URLs.
Acceptance criteria: Cache reads/writes are cancellable, survive process
restart, respect TTL, and prune by age or size.
Verification: Temp-dir tests; permission-failure tests; targeted tests for
`internal/storage` and `internal/config`.
Risks: Filesystem permissions vary; doctor should surface cache writability
without failing chat.
Follow-ups: Add ETag/Last-Modified support where providers expose it.

Task: Resolve Twitch avatars through Helix Get Users.
Owner lane: Twitch integration engineer.
Goal: Populate `profile_image_url` metadata for visible chat authors in
batches and cache it.
Context: `ChatMessage.AvatarURL` exists, but IRC messages do not provide it.
Files likely touched: `internal/twitch`, `internal/assets`, `internal/app`,
`internal/render`, `docs/terminal-images.md`.
Implementation notes: Batch by login/user ID, debounce visible-message
requests, cache positive and temporary failure results, and respect context
cancellation.
Acceptance criteria: Avatar fallback remains stable before/after lookup;
resolved metadata can be handed to the image pipeline.
Verification: Helix HTTP fake tests; app fake asset event tests; targeted
tests for `internal/twitch`, `internal/assets`, `internal/app`, and
`internal/render`.
Risks: Helix rate limits and missing users; avoid per-message lookup.
Follow-ups: Use token validation identity client where possible.

Task: Resolve Twitch emote and badge metadata.
Owner lane: Twitch integration engineer.
Goal: Convert IRC emote positions and badge IDs into cached asset references
with readable fallback text.
Context: Emote tokens and compact badge fallbacks render today; images and
provider metadata are missing.
Files likely touched: `internal/twitch`, `internal/assets`,
`internal/render`, `docs/terminal-images.md`.
Implementation notes: Keep IRC emote position parsing byte/rune-safe for
Twitch tag semantics and preserve fallback tokens exactly.
Acceptance criteria: Known emote IDs and badge set IDs resolve to image asset
refs; fallback rows do not corrupt Unicode text.
Verification: Unit tests for emote positions, badge metadata fixtures, cache
failures, and golden fallback rows.
Risks: Twitch emote URL templates and badge APIs differ by global/channel
scope.
Follow-ups: Add animated emote policy after static rendering works.

Task: Add emoji grapheme asset mapping.
Owner lane: Rendering engineer.
Goal: Detect standard emoji grapheme clusters and map them to optional image
assets while preserving native Unicode fallback.
Context: Reveal and wrapping are grapheme-safe, but image-backed emoji are not
implemented.
Files likely touched: `internal/assets`, `internal/render`,
`internal/animation`, `docs/terminal-images.md`.
Implementation notes: Use a maintained Unicode-aware approach if a dependency
is justified; otherwise keep this to detection and provider-independent asset
keys.
Acceptance criteria: Emoji clusters, modifiers, and ZWJ sequences remain intact
in fallback and reveal modes.
Verification: Unicode fixture tests; golden rows with emoji clusters; targeted
tests for `internal/render`, `internal/animation`, and `internal/assets`.
Risks: Unicode data maintenance can become a project of its own.
Follow-ups: Decide provider or local asset pack for image files.

### Phase 3: Terminal Image Rendering

Task: Implement terminal capability detection.
Owner lane: Terminal/platform engineer.
Goal: Decide when image rendering should be enabled, disabled, or degraded.
Context: `twi doctor` reports environment hints, but the app does not yet use
full capability decisions.
Files likely touched: `internal/app`, `internal/config`, `internal/render`,
`docs/terminal-images.md`.
Implementation notes: Combine config modes, terminal environment, true color
hints, cache writability, and explicit off/auto behavior. Do not require image
support for chat.
Acceptance criteria: `auto`, `off`, and explicit image modes produce
predictable app and doctor states.
Verification: Env matrix tests; doctor tests; manual Kitty/Ghostty/non-Kitty
checks.
Risks: Terminal detection is imperfect; users need an override.
Follow-ups: Add a visible degraded-mode status line when useful.

Task: Add Kitty-compatible image renderer.
Owner lane: Terminal/image engineer.
Goal: Render fixed-cell avatars, emotes, and emoji through the Kitty graphics
protocol when supported.
Context: `render.ImageRenderer` exists as an interface only.
Files likely touched: `internal/render`, `internal/assets`, `internal/app`,
`docs/terminal-images.md`.
Implementation notes: Render through asynchronous Bubble Tea commands and
cache returned terminal cells. Keep placeholders stable while images load or
fail.
Acceptance criteria: Image-capable terminals display avatars/emotes/emoji;
unsupported terminals keep initials/tokens/Unicode without layout shift.
Verification: Unit tests with fake renderer; manual Kitty/Ghostty image smoke;
non-Kitty fallback smoke.
Risks: Protocol behavior varies across terminals and multiplexers.
Follow-ups: Add image invalidation and resize handling refinements.

Task: Integrate asset events into the chat model.
Owner lane: Core TUI engineer.
Goal: Refresh visible rows as assets resolve without blocking input or
destroying scroll/composer state.
Context: Current rows are rendered from message state and fallback options
only.
Files likely touched: `internal/app`, `internal/render`,
`internal/assets`, `internal/animation`.
Implementation notes: Treat asset updates like normal app messages. Avoid
rerendering hidden history more than necessary.
Acceptance criteria: Late avatar/emote/emoji resolution updates visible rows
without scroll jumps or composer loss.
Verification: Fake resolver integration tests; resize and scroll tests;
manual image-mode smoke.
Risks: Excessive rerenders can make busy chats sluggish.
Follow-ups: Add viewport-aware prefetching.

### Phase 4: Multi-Channel And Interaction UX

Task: Introduce channel state model before joining multiple channels.
Owner lane: Core TUI engineer.
Goal: Preserve per-channel history, composer draft, reply target, connection
state, selected message, unread count, and send queue.
Context: CLI allows repeated `--channel`, and live chat now accepts multiple
configured channels.
Files likely touched: `internal/app`, `internal/cli`, `internal/twitch`,
`docs/product/requirements.md`, `README.md`.
Implementation notes: First refactor the model around a single active channel
using the new per-channel structure, then enable multiple channels.
Acceptance criteria: Existing one-channel behavior is unchanged and tests cover
two isolated fake channels.
Verification: `go test ./internal/app ./internal/cli ./internal/twitch`;
manual mock one-channel and multi-channel smokes.
Risks: A broad model refactor can regress send/reply state; keep the first
slice behavior-preserving.
Follow-ups: Add sidebar UI.

Task: Enable live multi-channel join and switching.
Owner lane: Twitch integration engineer.
Goal: Join multiple Twitch channels and switch active chat from the TUI.
Context: The IRC library can join multiple channels, and the app now routes
messages, unread counts, scroll, drafts, replies, and sends per channel.
Files likely touched: `internal/twitch`, `internal/app`, `internal/cli`,
`docs/quickstart.md`, `docs/auth.md`.
Implementation notes: Route incoming messages by normalized channel, keep
sends tied to active channel, and expose channel-level connection states where
available.
Acceptance criteria: Messages from two fake and live channels remain separate;
unread counts update when inactive.
Verification: Fake multi-channel tests; manual two-channel Twitch check.
Risks: Twitch connection events are connection-level, not per-channel; the app
copies them onto configured channel states.
Follow-ups: Add reconnect isolation if transport supports it.

Task: Add channel sidebar and keyboard navigation. Status: implemented for keyboard switching, normal/wide sidebar rendering, and narrow status collapse.
Owner lane: UX/TUI engineer.
Goal: Make multi-channel state visible and keyboard-first.
Context: Current layout focuses on status, viewport, composer, and help.
Files likely touched: `internal/app`, `internal/theme`, `docs/development.md`.
Implementation notes: Keep narrow layout usable by collapsing or hiding the
sidebar. Avoid making mouse required.
Acceptance criteria: Users can switch channels, see unread counts and
connection state, and keep drafts per channel.

Current implementation: `[` and `]` switch the active channel from chat focus.
Normal and wide layouts render a channel sidebar with active-channel,
connection, and unread indicators. Narrow layouts hide the sidebar and keep
channel count plus unread total in the status line.
Verification: Layout snapshot tests at narrow/normal/wide widths; key-binding
tests; manual resize check.
Risks: Terminal width pressure can make the UI noisy.
Follow-ups: Add command palette switcher for narrow screens.

Task: Add optional mouse support.
Owner lane: UX/TUI engineer.
Goal: Support mouse scroll, channel click, composer focus, and message select
without weakening keyboard workflows.
Context: Mouse is later scope and should be optional.
Files likely touched: `internal/app`, `internal/config`,
`docs/product/requirements.md`, `docs/config.md`.
Implementation notes: Gate mouse support behind config or terminal capability.
Keep behavior deterministic in tests.
Acceptance criteria: Mouse interactions work when enabled and all workflows
remain accessible by keyboard.
Verification: Bubble Tea mouse event tests; manual terminal mouse check.
Risks: Terminal mouse reporting varies; make it easy to disable.
Follow-ups: Add clickable URLs only after security/UX review.

Task: Add command palette and inspect panel.
Owner lane: UX/TUI engineer.
Goal: Provide discoverable actions and a focused way to inspect message,
author, badge, and raw diagnostic metadata.
Context: Raw IRC tags are retained only for diagnostics/debug views.
Files likely touched: `internal/app`, `internal/render`, `internal/twitch`,
`docs/development.md`.
Implementation notes: Redact any credential-like values before showing raw
metadata. Keep the main chat uncluttered.
Acceptance criteria: Palette can trigger common actions; inspect panel shows
safe metadata for selected messages.
Verification: Redaction tests; key-binding tests; snapshot tests.
Risks: Debug views can accidentally expose sensitive data if not filtered.
Follow-ups: Add copy/export only after a security pass.

### Phase 5: Login, Setup, And Secure Storage

Task: Implement `twi login` OAuth device flow or local callback flow.
Owner lane: Auth/platform engineer.
Goal: Let users obtain usable Twitch credentials without manually pasting
tokens.
Context: The CLI advertises `twi login`, but it currently returns planned/not
implemented.
Files likely touched: `internal/cli`, `internal/config`, `internal/twitch`,
`docs/auth.md`, `docs/quickstart.md`.
Implementation notes: Prefer an OAuth flow appropriate for terminal apps.
Request only required scopes first: `chat:read` and `chat:edit`.
Acceptance criteria: Login stores or prints next-step configuration safely and
doctor validates the resulting token.
Verification: HTTP fake flow tests; manual login with test account; secret
redaction search.
Risks: OAuth UX and Twitch app registration requirements can be confusing.
Follow-ups: Add account switch/logout.

Task: Add secure credential storage.
Owner lane: Auth/platform engineer.
Goal: Store tokens outside plain config where supported, with explicit fallback
behavior.
Context: Config files and env vars work, but long-lived tokens deserve safer
storage.
Files likely touched: `internal/config`, `internal/cli`, `docs/auth.md`,
`docs/config.md`.
Implementation notes: Evaluate OS keychain support against dependency and
maintenance cost. Keep env/config compatibility.
Acceptance criteria: `twi login` can save credentials securely on supported
platforms; unsupported platforms get clear fallback guidance.
Verification: Interface fake tests; platform-gated manual checks; redaction
tests.
Risks: Cross-platform keychain behavior can be brittle in CI and containers.
Follow-ups: Config migration helper.

Task: Add first-run setup wizard.
Owner lane: UX/platform engineer.
Goal: Guide new users through channel, auth, image mode, animation mode, and
doctor checks.
Context: Manual config is workable but not friendly.
Files likely touched: `internal/cli`, `internal/config`, `docs/quickstart.md`.
Implementation notes: Keep noninteractive flags/env viable for automation and
Docker.
Acceptance criteria: A new user can reach working mock mode and then live mode
from prompts without reading every doc.
Verification: CLI prompt tests with fake stdin/stdout; manual first-run smoke.
Risks: Prompt flows can block automation; provide `--non-interactive` or clear
command alternatives.
Follow-ups: Add config migration when schema changes.

### Phase 6: Release, Operations, And Quality Gates

Task: Add CI for formatting, tests, vet, race, static analysis, and vulnerability checks.
Owner lane: QA/release engineer.
Goal: Make the documented quality gate automatic.
Context: Tools are pinned, but no CI workflow is part of the current source
tree.
Files likely touched: `.github/workflows/*`, `docs/development.md`,
`README.md`.
Implementation notes: Keep network-heavy or credentialed checks separate from
default pull request gates.
Acceptance criteria: Pull requests run `go fmt`/diff check, `go vet ./...`,
`go test ./...`, targeted race tests, `go tool staticcheck ./...`, and
`go tool govulncheck ./...` where feasible.
Verification: GitHub Actions run; local equivalent commands.
Risks: Latest Go/toolchain availability can lag in hosted runners.
Follow-ups: Add release workflow after the gates are stable.

Task: Add release packaging.
Owner lane: QA/release engineer.
Goal: Build reproducible binaries and container images for supported
platforms.
Context: Docker files exist locally, but release artifacts are not defined.
Files likely touched: `Dockerfile`, `compose.yaml`, `.github/workflows/*`,
`docs/docker.md`, `README.md`.
Implementation notes: Publish checksums, SBOM/provenance if practical, and
private-image guidance before public distribution decisions.
Acceptance criteria: Tagged builds produce binaries and a container image that
can run `twi --help`, `twi doctor`, and mock chat.
Verification: Release dry run; container smoke; checksum validation.
Risks: Terminal TUI behavior inside containers needs clear documentation.
Follow-ups: Package manager manifests only after CLI stabilizes.

Task: Add observability and debug logging controls.
Owner lane: Platform engineer.
Goal: Help users debug connection, asset, and render issues without leaking
secrets or cluttering chat.
Context: Diagnostics exist, but runtime logs/debug views are minimal.
Files likely touched: `internal/app`, `internal/config`, `internal/twitch`,
`docs/development.md`, `docs/auth.md`.
Implementation notes: Default to quiet. Redact credentials and raw OAuth-like
strings. Keep logs out of the main TUI unless explicitly requested.
Acceptance criteria: Users can enable debug logs for auth/connect/assets with
redacted output and no secrets in tests.
Verification: Redaction tests; secret-pattern search; manual debug run.
Risks: Logs can capture user chat content; document privacy implications.
Follow-ups: Add structured bug-report bundle only after privacy review.

## Cross-Phase Rules

- Each feature slice must update the relevant docs in the same change.
- Each behavior change must include focused tests near the affected package.
- Keep `internal/app` free of concrete Twitch IRC or Helix client types.
- Keep network and filesystem work out of Bubble Tea `Update` and `View`
  paths unless it is explicitly asynchronous and cancellable.
- Preserve text fallbacks for every image and asset feature.
- Redact OAuth tokens, refresh tokens, client secrets, and credential-looking
  values in errors, config output, doctor output, logs, tests, and snapshots.
- Prefer `go fmt ./...`, `go vet ./...`, and `go test ./...` as the default
  handoff gate; add race/static/vulnerability checks for wider changes.
