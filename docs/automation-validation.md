# PLAN implementation and verification

Updated 2026-10-12 after the user's instruction to finish all implementation gaps.
The user explicitly chose Twitch as the only network message source. No
third-party history service, undocumented backfill endpoint, or hidden recorder
was added. Local captured history, FTS search, and explicit TUI attachment are
supported.

## Implemented

- Per-invocation CLI dependencies and parallel-safe tests; shared producer use
  case; validated channel values; config independent of rendering packages.
- Shared production IRC/EventSub lifecycle for TUI and automation, readiness,
  per-outage retry budgets, manual reconnect cancellation, and loss reporting.
- Bubble Tea v2.1.0 / Lip Gloss v2.0.6, native views/input, extracted keyboard and
  rendering controllers, safe paste, read-only sources, recorder feedback.
- Typed HTTP status/quota errors; bounded cancellable header-based 429 retries;
  no retry of ambiguous sends; fresh credentials on each read retry.
- Private account-scoped normal and verified privileged quotas; shared local
  channel leases; named accounts with explicit credential precedence.
- SQLite batches up to 100 records/100ms, schema 7 privacy repair with FTS search, moderation/prune
  privacy, explicit TUI recording, and read-only TUI replay/follow attachment.
- EventSub messages and notifications preserve structured fragments, badges,
  source identity, replies, and message types; clears/deletes, held-message
  status, and scope/role-gated moderation preserve typed targets and bounded
  setting details. Removed notice bodies are redacted alongside chat messages.
- Immutable action commits, target-specific CycloneDX SBOMs, unsigned SLSA
  provenance, aggregate checksum verification, four static release targets,
  nonroot recorder container signal/permission checks, and a real Snap package.

Agents implemented independent slices, then rotated into reviewer, bug-hunter,
and judge roles. Review findings led to fixes for lost terminal errors, blocked
reconnect shutdown, final-flush errors, final-until coverage, notice identity,
recorder overflow gaps, role-proof revocation, saturated terminal-state queues,
empty-selection reconnect deadlines, and notice redaction/metadata ownership.

## Verified implementation boundaries

Tests cover strict input/routing, text/UTF-8/Unicode limits, HTTP acknowledgement
and ambiguity, rate-header waits/cancellation, shared mixed quota windows,
account isolation, startup/health/reconnect/cancellation, safe output, FTS
privacy, bounded batches/pages, checkpoint duplicate recovery, and TUI protocol
behavior. Actual fault tests include corrupt databases, killed writers and
migration transactions, output-before-checkpoint process death, pruning between
scan/emission, and real ENOSPC on an isolated 8 MiB tmpfs.

The measured quiet/busy/slow-consumer workload has enforced numeric regression
budgets. See [history performance](history-performance.md). The native PTY
matrix checks keyboard, paste, Unicode, focus, mouse, resize, read-only drafts,
and terminal/background/cursor/termios restoration. See [UI v2 validation](ui-v2-validation.md).

Local gates passed: tests/race, vet, staticcheck, vulnerability scan, modules,
formatting/whitespace checks. Four CGO-disabled Linux/Darwin amd64/arm64 binaries
built with checksums. Container recorder TERM/INT and permission checks passed.
Snapcraft packed the actual package; packaged binary and metadata smokes passed.
See [release evidence](release.md) for reproducible commands and artifact scope.

## Live Twitch evidence

User authorized account/channel `worxbend`. Provided credentials were saved only
in private Git-ignored `.env`. The original access token was rejected; refresh
succeeded and returned a matching user/client identity. No token or client
secret appears in evidence or public output.

Live checks passed with EventSub read/send grants:

- Subscription readiness and receipt acknowledgement.
- EventSub TUI launch/input and terminal restoration on q, Ctrl+C, and SIGTERM
  using the existing read grant (no composer sends).
- Reply delivery and a 500-character message containing an astral Unicode emoji.
- Same-account messages observed by the separate recorder.
- Received reply parent identity, local FTS search, recorder SIGTERM 143, and a
  clean persisted capture boundary.

The additional device OAuth grant succeeded for the authorized account. Actual
IRC and EventSub suites passed acknowledged user sending, replies, Unicode500,
separate-recorder observation, deletion receipt, persisted body redaction,
FTS removal, SIGTERM143 and clean boundaries. EventSub enhanced moderation
also preserved the authenticated moderator’s identity. All earlier three test
messages and all nine additional test messages were deleted by explicit ID.

A read-only live suite forced closure of a real subscribed EventSub connection
and verified a replacement Twitch subscription, capture gap, recovered readiness
and bounded cancellation. Two concurrent recorder processes started from the
same deliberately invalid saved access token, recovered through real OAuth
refresh, and produced exactly one persisted rotation with valid identity and
clean capture shutdowns. The current rotated pair remains saved privately.

App-token sends passed delegated authorization, source-only/default and explicit
all routing receipts in a verified standalone room. A native IRC action produced
an honest unconfirmed receipt and was observed as a normalized action by the
separate EventSub recorder. These three mode-test messages were deleted and
redacted. Multi-room shared-chat validation was explicitly skipped by the user;
standalone routing does not demonstrate multi-room propagation.

Installed Snap launcher testing passed in an isolated KVM Ubuntu24 guest with
snapd: actual package installation, launcher checks, and removal. The host
required interactive sudo and remained unchanged. A reproducible helper verifies
the official guest image checksum and confines the guest and artifacts to a
temporary directory. See [release evidence](release.md). Unsigned provenance
records build evidence; it is not an attestation signature.

Schema7 repair regressions cover legacy tombstones, user/channel boundaries,
previously ignored `user_messages_cleared` controls, alias/reply removal, delayed
identityless/recovered-ID notices, read-only privacy, and unchanged store identity
and sequence numbers. `history upgrade` is explicit, offline, cancellable, and
requires an existing database; it preserves retention policy.

A20 now has actual Bubble Tea loop/renderer CPU and allocation profiles, GC-live
per-channel memory, redraw/write rates, event age, bounded ingress queues and a
delayed-terminal workload. See [TUI performance](tui-performance.md).

Final local tests/race, vet, staticcheck, vulnerability scan, module verification,
idempotent tidy, whitespace checks and the PTY matrix passed.

The final A20 profiling cohort `/tmp/twi-tui-performance-final-module` passed all
budgets with unchanged source. Its Go-source/module digest is
`803a91335b31614b24891bfc15f5cd7cf3a7baae5a4c40ec2391481f6e1d7c10`.

Hosted [CI](https://github.com/worxbend/twi/actions/runs/38244841851),
[release preparation](https://github.com/worxbend/twi/actions/runs/38244874857), and
[Snap build/install smoke](https://github.com/worxbend/twi/actions/runs/38244871454)
all passed at `490c9a3d9ce03a76cc83c4fe9c0562ea2bd3b5f1`. Publication steps were skipped.
See [hosted validation](hosted-validation.md) for snapshot and downloaded-artifact
verification details. No remaining required implementation or validation item is
open under the documented contracts; live multi-room shared-chat validation was
explicitly skipped by the user.
