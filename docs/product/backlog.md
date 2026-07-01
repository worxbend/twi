# Initial Agent Backlog

Status: First focused agent-loop tasks derived from `PLAN.md`.

Progress as of the initial swarm pass:

- Done: Phase 0 requirements matrix, risk register, backlog, and six ADRs.
- Done: Go module bootstrap, CLI shell, config precedence/redaction tests, normalized message model skeleton, Bubble Tea mock chat shell, and module tool directives for `govulncheck`/`staticcheck`.
- Remaining near-term work: rendering/motion checkpoint validation and the real Twitch IRC adapter.

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
Context: MVP requires credentials and modes from flags/env/config with no secret leakage.
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

Task: Implement composer send path and send queue.
Owner lane: Twitch integration engineer.
Goal: Send messages from the TUI without losing user text on failure.
Context: MVP needs IRC `Say` for the active channel and visible send status.
Files likely touched: `internal/app`, `internal/twitch`.
Implementation notes: Queue sends through commands; clear composer only after chosen queued/accepted state; expose rate-limit feedback.
Acceptance criteria: User can send a message to the active channel; failures show a reason and preserve text.
Verification: Fake sender success/failure tests; manual send to test channel; `go test ./internal/app ./internal/twitch`.
Risks: Twitch send restrictions may vary by account/channel.
Follow-ups: Add replies and `/me`.

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
Implementation notes: Define asset references, cache interface, image renderer interface, and no-image fallback behavior. Do not download assets in `View`.
Acceptance criteria: Renderer can request asset placeholders and render fallbacks when assets are missing, disabled, or unsupported.
Verification: Fake cache/renderer tests; fallback golden snapshots; code search for I/O in render/view paths.
Risks: Designing too much before real assets; keep interfaces minimal.
Follow-ups: Add Helix avatar lookup and Kitty renderer.

## Task 12

Task: Add first `twi doctor` diagnostics skeleton.
Owner lane: QA/release engineer.
Goal: Give users and agents a single command for setup visibility.
Context: Full diagnostics are later scope, but early checks reduce support ambiguity.
Files likely touched: `cmd/twi`, `internal/config`, `internal/diagnostics`.
Implementation notes: Start with config path, credential presence without values, Go/runtime info, terminal env hints, and redacted output.
Acceptance criteria: `twi doctor` runs without Twitch credentials and never prints secrets.
Verification: Unit tests for diagnostic redaction; manual `go run ./cmd/twi doctor`.
Risks: Terminal feature detection may be incomplete initially.
Follow-ups: Add token validation, Twitch reachability, mouse, true color, Kitty graphics, and cache writability checks.
