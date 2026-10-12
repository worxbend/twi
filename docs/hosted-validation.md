# Hosted validation snapshot

The source checkout stays on its existing branch with its uncommitted changes.
Hosted testing uses a separate clone, a task-only snapshot commit, and an isolated
`codex/plan-validation-*` branch. This includes the already requested dependency
refresh and the completed automation/UI/history changes. It creates no tag,
release, pull request, comment, or Snap publication.

Prepare only after all implementors confirm that source is stable:

```sh
python3 scripts/operations/snapshot-hosted-validation.py --directory /tmp/twi-hosted-snapshot/repo
# Add --push only when explicitly running the hosted validation task.
python3 scripts/operations/snapshot-hosted-validation.py --directory /tmp/twi-hosted-snapshot-final/repo --push
```

The snapshot helper starts from the current repository revision and overlays an
explicit source/docs/scripts/examples/workflow allowlist. It includes tracked
changes and approved untracked implementation files, preserves deletions, rejects
symlinks, and excludes `.env`, credentials, recorded SQLite state, screenshots,
logs, binaries, and local agent state. It checks that the source branch, revision,
and worktree status were not changed by the snapshot operation. When private known-secret inputs are present, preflight scans approved source
files against access/refresh tokens and client secrets; it prints no values or
file contents. Public client IDs are excluded. The clone's
origin must match this project's GitHub repository before a branch push.

The CI workflow accepts that dedicated branch prefix. Pushing its snapshot starts
the same hosted tests, race/static/vulnerability gates, four-target builds,
SBOM/provenance/checksum generation, Docker recorder/disk-full checks, supervisor
parsers, and PTY matrix used by normal CI. Record the exact snapshot SHA and run
URLs in the evidence so results refer to a concrete source tree.

The existing manual Snap workflow can separately build, install, exercise and
remove its package on a hosted runner:

```sh
gh workflow run snap.yml --repo worxbend/twi --ref codex/plan-validation-YYYYMMDD-HHMMSS
gh run list --repo worxbend/twi --branch codex/plan-validation-YYYYMMDD-HHMMSS
gh run watch RUN_ID --repo worxbend/twi --exit-status
```

Its publishing steps require a tag/branch push event; manual validation does not
satisfy those conditions. Do not dispatch a tag or create a release for testing.
Credentials used to authenticate the GitHub operation stay in the host's existing
credential manager; they are never added to the snapshot.

## Final hosted evidence, 2026-10-12

Validation branch `codex/plan-validation-20261012-114056`, exact source commit
`490c9a3d9ce03a76cc83c4fe9c0562ea2bd3b5f1`:

- [CI](https://github.com/worxbend/twi/actions/runs/38244841851): passed formatting/tidy, vet, tests/race, vulnerability/static analysis, CLI/PTY, four-target artifact metadata, Docker recorder signals/permissions, real ENOSPC, and supervisor parsers.
- [Release preparation](https://github.com/worxbend/twi/actions/runs/38244874857): passed four-target build, SBOM/provenance/checksums and artifact upload. The GitHub publication step was skipped.
- [Snap](https://github.com/worxbend/twi/actions/runs/38244871454): passed real package build, payload validation, installed launcher smoke and artifact upload. Edge/stable publishing steps were skipped.

Downloaded release artifacts are in `/tmp/twi-hosted-final-artifacts/twi-release-dry-run`.
Their provenance records this exact revision; subject hashes match the downloaded
binaries and SBOMs. The first validation snapshot failed only because the new
profiling import required marking `colorprofile` direct in `go.mod`. The declaration
was corrected without a version change; final hosted tidy and all subsequent gates
passed. Earlier-SHA manual runs were cancelled and do not establish final evidence.

The source checkout remains on `main` at `b924905`, with the requested changes
kept in its existing working tree. Snapshot commits and the validation push were
made in an isolated clone. No tag, release, Snap publication, PR or comment was
created. The initial source snapshot and follow-up update passed known-secret
scans and preserved source HEAD/branch/worktree state.
