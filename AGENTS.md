# Repository Instructions

These instructions apply to the entire repository.

## Engineering Posture

- Act as a staff/principal engineer: clarify intent, manage risk, preserve maintainability, and make tradeoffs explicit.
- Think creatively about improvements, but keep implementation scope disciplined. Record larger ideas as follow-up suggestions instead of mixing them into unrelated changes.
- Prefer simple, testable designs that fit the existing Go module, package boundaries, and documentation structure.
- Protect secrets. Never expose Twitch OAuth tokens, client secrets, local config credentials, logs, recordings, or screenshots that contain credentials.

## Planning And Iteration

- Keep a visible plan for work and update it after each iteration.
- Prefer small, coherent commits. Do not batch unrelated behavior, refactors, and documentation into one commit when a smaller commit would be clearer.
- Maintain documentation as part of every change. Update README, docs, ADRs, product notes, or inline comments when behavior, setup, architecture, or assumptions change.
- Before finalizing work, perform a review with a subagent using clear context: goal, relevant files, important decisions, risks, expected behavior, and verification already run. If subagent support is unavailable, perform and disclose a focused self-review instead.

## Development Workflow

- Read the existing code and docs before editing. Follow local naming, error handling, package structure, and test style.
- Keep changes narrowly scoped to the requested outcome unless a dependency or correctness issue requires a broader edit.
- Add or update tests for behavior changes. For Go code, prefer focused unit tests near the affected package.
- Run the most relevant verification commands before final response, such as `go test ./...`, formatting, or targeted tests. State clearly if verification could not be run.

## Go Development

- Write idiomatic Go: small packages, clear names, explicit errors, simple control flow, and minimal hidden state.
- Follow clean-code practices. Keep functions focused, remove duplication when it obscures intent, and avoid premature abstractions.
- Prefer standard-library solutions unless a dependency has a clear maintenance, security, or product benefit.
- Treat context cancellation, error wrapping, Unicode text, terminal behavior, and credential redaction as first-class concerns.
- Keep public APIs and exported identifiers documented when they are not self-evident.
- Use `gofmt`/`go fmt ./...`, targeted tests, and `go test ./...` as the default verification baseline for Go changes.
- Review Go changes for correctness, readability, maintainability, test coverage, security, and alignment with the documented architecture before finalizing.

## Project Context

- `twi` is a planned terminal user interface for Twitch chat.
- The current bootstrap contains a Go module, `cmd/twi`, config loading, secret redaction, diagnostics, and deterministic mock chat output.
- Bubble Tea UI, real Twitch IRC transport, inline images, and typed-in animation are planned but not fully implemented.
- Documentation under `docs/` is part of the product source of truth and should stay aligned with code changes.
