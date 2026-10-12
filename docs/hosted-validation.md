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
