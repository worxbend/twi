# 0006: Pin Latest Stable Go Toolchain And Module Tools

## Status

Accepted for MVP.

## Context

`PLAN.md` requires current official Go tooling, Go modules, and pinned module tools. The current stable Go release was verified from `https://go.dev/dl/` as `go1.26.4` on 2026-07-01. The local toolchain reported by the task context is `go1.26.0`.

The project should use stable Go language and module semantics while allowing developers with older local patch releases to use the suggested toolchain automatically.

## Decision

Use Go modules only and set the project baseline to Go 1.26 language semantics with the latest verified stable patch toolchain.

When `go.mod` is created or updated, use:

```go
go 1.26

toolchain go1.26.4
```

Keep `GOTOOLCHAIN=auto` compatible so a developer on `go1.26.0` can automatically use the suggested `go1.26.4` toolchain.

Manage project tools through Go module `tool` directives instead of unmanaged global binaries. Add tools with `go get -tool`, starting with:

- `golang.org/x/vuln/cmd/govulncheck`
- `honnef.co/go/tools/cmd/staticcheck` if it works cleanly with the selected Go release

Use `go tool <name>` for pinned tools declared in `go.mod`.

Before adding dependencies, check whether the standard library or existing dependency set already solves the problem. When a dependency is needed, add it with `go get <module>@latest`, review the selected version and transitive impact, then keep `go.mod` and `go.sum` machine-managed through Go commands.

If implementation starts after a newer stable Go release is verified, update the toolchain decision deliberately and record the verification date.

## Consequences

- The module has a clear, reproducible toolchain baseline.
- Developers with older local patch releases can still work when automatic toolchain download is allowed.
- Tools such as `govulncheck` and `staticcheck` are reproducible project inputs rather than implicit machine state.
- CI and local development should use the same module and tool directives.
- Toolchain updates require an explicit verification step instead of ad hoc edits.

## Verification

### 2026-10-11 update

The official [Go release feed](https://go.dev/dl/?mode=json) was checked again.
The baseline is now `go 1.27.0` with `toolchain go1.27.2`. Docker and the Snap
Go channel are aligned with this release. Module tools were refreshed to
`golang.org/x/vuln v1.9.0` and `honnef.co/go/tools v0.8.1`; both run successfully
with the new toolchain. The original dated decision above is retained as history.

- Run `go version` before implementation work that depends on the toolchain.
- Run `go mod tidy` after dependency or tool changes.
- Run `go fmt ./...`.
- Run `go test ./...`.
- Run `go test -race ./...` for concurrency, networking, asset cache, and animation scheduler changes.
- Run `go vet ./...`.
- Run `go list -m -u all` during dependency review.
- Run `go tool govulncheck ./...` after `govulncheck` is added as a module tool.
- Run `go tool staticcheck ./...` if `staticcheck` is added and compatible.
