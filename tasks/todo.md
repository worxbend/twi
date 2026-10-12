# PLAN completion checklist

- [x] CLI per-invocation dependencies and parallel-safe tests; shared producer use case.
- [x] TUI/headless shared lifecycle; domain Channel; config/UI dependency separation.
- [x] Bubble Tea/Lip Gloss v2 with input/render/PTY parity and controller extraction.
- [x] Typed HTTP/quota errors; bounded cancellable explicit-429 retries; ambiguity preserved.
- [x] Recorder batches; FTS history search; private named account selection.
- [x] Explicit TUI recording and read-only attachment to existing history.
- [x] Broader EventSub chat notices/moderation parity and subscription capability checks.
- [x] Actual crash/migration/corruption/disk-full/checkpoint/prune acceptance tests.
- [x] Quiet/busy/slow consumer performance baseline and numeric budgets.
- [x] Immutable CI action SHAs; release SBOM/provenance/checksum verification.
- [x] Docker recorder nonroot/signals/permissions and Snap package build/smoke.
- [x] Live EventSub readiness/send/reply/Unicode/self-message/history/clean-boundary evidence.
- [x] Additional live IRC/deletion validation and cleanup: device consent and real checks passed.
- [x] Installed Snap launcher validation: isolated KVM guest/snapd install, checks and removal passed.
- [x] Full tests/race/vet/static/vulnerability/modules, four builds, artifact/docs reconciliation.

Explicit user exclusion: no third-party history provider or private/undocumented
Twitch backfill endpoint. Twitch does not expose arbitrary earlier chat history.

Live EventSub readiness/send/reply/Unicode500/same-account recording/search/SIGTERM
checks passed. IRC/moderation consent completed; live checks and all test-message cleanup passed. Snap
package/payload checks passed; installed launcher passed in an isolated guest without host installation.

Final reviewed artifacts: /tmp/twi-final-reviewed-release (four targets, SBOMs,
unsigned provenance, SHA256SUMS); Docker twi:final-reviewed; Snap
/tmp/twi-plan-snap/twi_amd64.snap. Earlier marked Twitch validation messages were deleted204; IDs and cleanup
statuses are in the private redacted report
/tmp/twi-live-validation-redacted.json. Root checkout HEAD preserved; isolated validation snapshot commits/push performed.
No release or Snap publication performed.

Follow-up completion requested by user:
- [x] Device OAuth authorization with private persistence and validated scopes.
- [x] App-token sending with delegated grants and source/all routing contracts.
- [x] Explicit IRC action sends with truthful unconfirmed receipts.
- [x] Live action/app sends and exact-ID cleanup.
- [x] Real EventSub reconnect and capture-gap validation.
- [x] Real two-process OAuth refresh with one persisted rotation.
- [x] Explicit systemd/launchd/Compose supervisor examples.
- [x] Final-source A20 TUI CPU/allocation/memory/redraw profiling cohort.
- [x] Schema7 legacy-notice privacy migration/read-only protections and regression checks.
- [x] Explicit existing-store offline upgrade with cancellation and unchanged retention.
- [x] Final-source hosted CI/release/Snap build workflows and artifact verification.
- [x] Final local gates, refreshed binaries/package and evidence reconciliation.
- [x] Multi-room shared-chat live check skipped at the user's explicit request.

Hosted closure: `codex/plan-validation-20261012-114056` at `490c9a3d9ce03a76cc83c4fe9c0562ea2bd3b5f1`.
CI 38244841851, Release 38244874857, Snap 38244871454 all success; publication skipped.
Final profiling: /tmp/twi-tui-performance-final-module, source unchanged, no budget
failures. Root Go sources/modules match tested snapshot; final evidence docs were
updated locally after runs.
