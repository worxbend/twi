# Release Packaging

This project uses a credential-free release dry-run for binary and container
packaging. It is separate from the default pull-request CI gate because Docker
runtime checks, terminal behavior, and credentialed Twitch checks have different
environment needs.

## Supported Binary Targets

The release script builds these targets by default:

| GOOS | GOARCH | Artifact |
| --- | --- | --- |
| linux | amd64 | `twi_linux_amd64` |
| linux | arm64 | `twi_linux_arm64` |
| darwin | amd64 | `twi_darwin_amd64` |
| darwin | arm64 | `twi_darwin_arm64` |
| windows | amd64 | `twi_windows_amd64.exe` |

Each binary is built with:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" ./cmd/twi
```

The script writes a sibling `.sha256` file for each binary and verifies the
checksum before continuing.

## Local Dry Run

Run the full release dry-run from the repository root:

```sh
scripts/release-dry-run.sh --out /tmp/twi-release --image twi:local
```

The default output directory is `dist/release`, which is ignored by Git. Use
`--skip-docker` only when Docker is unavailable and you want to validate binary
packaging without claiming container coverage.

The script reads the `toolchain` directive from `go.mod`, uses `GOTOOLCHAIN=auto`
unless already overridden, and passes the same Go version to the Docker build as
`GO_VERSION`. It does not add package manifests, global tools, or unmanaged
release dependencies. Native binary smokes run with temporary isolated
`XDG_CONFIG_HOME` and `XDG_CACHE_HOME` directories, plus empty Twitch credential
environment variables, so local config files and saved credentials are not read.

## Automated Checks

The local script and `.github/workflows/release.yml` perform these checks:

- Build trimmed binaries for the supported target matrix.
- Write and verify SHA-256 checksum files.
- Smoke the native binary with `twi --help`, `twi doctor`, and
  `twi chat --mock --channel example`.
- Build the Docker image from the repository Dockerfile.
- Smoke the image with `twi --help`, `twi doctor`, and mock chat.

The workflow is triggered by `workflow_dispatch` or `v*` tag pushes. It uploads
the dry-run artifacts as a workflow artifact. It is not a pull-request trigger.

## Secret Handling

Release builds must not embed Twitch credentials. The Dockerfile copies only
`go.mod`, `go.sum`, `cmd/`, and `internal/` into the build stage. The
`.dockerignore` file excludes local environment files, `.local` config mounts,
root config/credential JSON files, build outputs, logs, screenshots, recordings,
and agent state from the build context. The release script clears both `TWI_*`
and short `TWITCH_*` credential environment variables for binary and container
smokes, and passes empty credential values explicitly to `docker run`.

Do not add `--build-arg`, `ENV`, or copied files that contain OAuth tokens,
refresh tokens, client secrets, callback codes, authorization URLs, debug logs,
screenshots, or local config credentials.

## Manual Or Credentialed Checks

The release dry-run does not prove:

- Credentialed Twitch IRC read/send/reconnect behavior.
- Browser-based `twi login` against a real Twitch app.
- Real Kitty/Ghostty inline image drawing in a compatible terminal.
- Interactive pointer behavior in a physical terminal.
- Registry publishing, signing, notarization, or package-manager installs.

Record those checks in [manual-validation.md](manual-validation.md) when the
right terminal and credentials are available. If an environment is unavailable,
document the skip reason instead of claiming support.
