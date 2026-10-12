# Release packaging and verification

The release dry-run builds the four supported binary targets with the pinned
Go toolchain, `CGO_ENABLED=0`, trimmed source paths, and an explicit version.
It generates target-specific SBOMs and provenance, verifies checksums, and tests
native and Docker behavior without Twitch credentials. Packaging tests never
load `.env` or the user's saved credentials.

| Target | Binary |
| --- | --- |
| Linux amd64 | `twi_linux_amd64` |
| Linux arm64 | `twi_linux_arm64` |
| macOS amd64 | `twi_darwin_amd64` |
| macOS arm64 | `twi_darwin_arm64` |

```sh
scripts/release-dry-run.sh --out /tmp/twi-release --image twi:local
# Binary/SBOM/provenance checks without container validation:
scripts/release-dry-run.sh --out /tmp/twi-release --skip-docker
```

The default directory is ignored `dist/release`. A `--targets` space-separated
list can narrow the matrix. `scripts/check-toolchain.sh` requires the active Go
version and Docker default to match `go.mod`, and the Snap Go track to match that
version's major/minor family. Snap builds additionally reject a mismatched patch
version. This keeps toolchain drift visible when a Snap channel changes.

## SBOMs, provenance, and checksums

Every binary has a `.sbom.cdx.json` CycloneDX 1.6 document generated from its
embedded Go build information. It includes the exact linked module identities,
Go authenticated module hashes, the Go standard library version, and target.
The application's MIT license comes from this repository's license; the generator
does not infer dependency licenses. Go `h1` hashes identify authenticated module
content and are not archive download checksums.

Each `.provenance.json` is an in-toto Statement v1 with a SLSA provenance v1
predicate. Its subjects bind the binary and SBOM by SHA-256. It records version,
target, Go version, `CGO_ENABLED=0`, source revision, dirty-source status, and a
source tree digest over explicit build inputs. Local statements are unsigned
and carry no independently attested SLSA-level guarantee. They omit environment
dumps, credentials, `.env`, local configuration, and recorded chat.

Sibling `.sha256` files and aggregate `SHA256SUMS` cover binaries, metadata,
and the release installer.
Verify the complete set before installing:

```sh
cd /tmp/twi-release
sha256sum -c SHA256SUMS
install -m 0755 twi_linux_amd64 "$HOME/.local/bin/twi"
```

On macOS use `shasum -a 256 -c SHA256SUMS`. The tag release workflow uploads all
four binaries, their metadata/checksums, the aggregate manifest, and `install.sh`.
The existing Linux installer verifies the downloaded binary's sibling checksum.
These checks establish integrity relative to the downloaded manifest; repository
release access remains the trust boundary.

Specification references: [CycloneDX 1.6 JSON schema](https://github.com/CycloneDX/specification/blob/master/schema/bom-1.6.schema.json),
[SLSA provenance v1](https://slsa.dev/spec/v1.0/provenance), and
[Go build information](https://pkg.go.dev/runtime/debug#BuildInfo).

## Immutable workflow actions

All external workflow actions use full commit SHAs, with release tags retained
as comments. `.github/action-pins.json` records the reviewed mapping. Annotated
Snap action tags are resolved to their underlying commits. CI rejects floating
refs or changes that disagree with the mapping.

```sh
python3 scripts/verify-action-pins.py
# Verify each upstream tag still points to the reviewed commit:
python3 scripts/verify-action-pins.py --online
```

The pins were checked against each upstream repository on 2026-10-12. Pinning
follows [GitHub's secure action-use guidance](https://docs.github.com/en/actions/reference/security/secure-use#using-third-party-actions).
Dependency updates should change the reviewed manifest and workflow references
together after verifying the upstream release.

## Container recording

The image runs as UID/GID 10001 and exposes separate config, cache, and state
locations. The explicit Compose recorder service persists all three, uses an
init process, a read-only root filesystem, and a 15-second signal grace period.
Only that explicit live service reads the optional local credential environment
file; offline packaging scripts use isolated fixtures.

```sh
docker build -t twi:local .
docker run --rm --network none twi:local --help
docker compose run --rm recorder
```

Set `TWITCH_CHANNEL` and the documented Twitch credentials when starting a live
recorder. It remains a foreground process. State belongs in the mounted `/state`
volume. Never put credentials into image layers or bake configuration into the
build context.

The release dry-run includes:

- Native and network-disabled image help, doctor, and mock chat checks.
- `scripts/container-recorder-smoke.sh`: the real `runRecord` command with an
  injected per-invocation scripted reader, nonroot SIGTERM/SIGINT cancellation,
  committed observed history, clean shutdown, private 0600/0700 state, and
  rejection of an unwritable history location. It makes no Twitch requests.
- `scripts/history-disk-full-smoke.sh`: real `ENOSPC` in a fresh 8 MiB container
  tmpfs, preserving committed history while stopping recording. The host disk
  is never filled.

## Snap build and smoke

The core24 classic snap uses the Go plugin, pinned-toolchain validation, version
injection, and static compilation. The 1.27/candidate channel currently supplies
Go 1.27.2; switch to stable when that exact toolchain is available there.

```sh
# Standard Snapcraft/LXD build, using a sanitized source copy:
scripts/snap-build-smoke.sh --out /tmp/twi-snap
# Isolated Ubuntu 24.04 Docker fallback without installing LXD/snapd on the host:
scripts/snap-build-smoke.sh --docker --out /tmp/twi-snap
# Repeat package payload smoke without rebuilding:
scripts/snap-smoke.sh /tmp/twi-snap/twi_amd64.snap
```

The Docker fallback uses a pinned Canonical Snapcraft image and mounts the
project's exact Go toolchain read-only instead of installing Go as a build snap.
It modifies only that toolchain-provisioning detail in a temporary recipe copy.
The build inputs are copied from an allowlist; `.env`, credentials, recordings,
and local run reports are excluded. This fallback requires Docker, Python/PyYAML,
Go, and `unsquashfs`. It builds the native amd64 Snap package.

Payload checks validate metadata, version, static build information, help,
doctor, dry-run production, and mock TUI operation with isolated config/state.
The manual Snap workflow additionally installs the package with snapd, checks
the installed launcher, and removes it. This validation does not publish a Snap.
Store name registration, publishing credentials, and classic-confinement review
remain separate release-administration requirements.

On 2026-10-12 an actual Docker Snapcraft build and packaged payload smoke passed.
The host sudo/LXD route required interactive authentication. Installed-launcher
validation then passed in an isolated Ubuntu 24.04 KVM guest: the actual package
was installed with snapd, its launcher passed help/version/doctor/dry-run produce/
record help/mock checks, and the package was removed. The official Ubuntu cloud
image SHA-256 was verified. Only the Snap artifact entered the guest; no host
credentials, `.env`, or source checkout was copied. Reproduce with
`scripts/operations/snap-launcher-vm-smoke.sh /tmp/twi-plan-snap/twi_amd64.snap`.
The produced package is available in `/tmp/twi-plan-snap/twi_amd64.snap`; no host
Snap was installed.

Final review builds use `/tmp/twi-final-reviewed-release` and image
`twi:final-reviewed`. Their aggregate manifest is
`/tmp/twi-final-reviewed-release/SHA256SUMS`. The final Snap build uses the same
reviewed source, including notification-body moderation redaction and terminal
state/readiness fixes.
