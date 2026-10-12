# ADR 0010: explicit local accounts and a shared live runtime

Status: accepted, implemented 2026-10-12.

## Context

Automation and the terminal need the same transport ownership, readiness, retry,
coverage, authorization, and acknowledged-send behavior. Mutable package-level
command factories prevent independent command tests from running safely in
parallel. Theme `profile` names already have a different meaning from accounts.

## Decision

Each CLI invocation creates its own dependency runner. Tests override that
runner, not shared globals. Configuration contains flat values and a plain
palette; it does not depend on terminal rendering or animation packages.

`internal/chatservice` owns live transport sessions, startup deadlines, channel
readiness, observed gaps, retries, and terminal capability failures. Sessions
separate startup cancellation from connection lifetime and close transports once.
Both production IRC and EventSub terminal adapters consume this service. The IRC
adapter forwards optional sending and runtime joins without owning a second
connection lifecycle. Manual terminal reconnect cancels and drains the prior
service generation before installing another. Retry budgets reset when the
selected channels regain readiness.

`internal/producer` owns authorization checks, channel resolution, shared send
permits, acknowledgements, and outcome classification. Input parsing, output
formatting, and concrete HTTP adapters remain outside this use case. A definitive
401 permits one refresh and retry. Ambiguous writes never retry. HTTP 429 retries
are bounded by the selected transport policy and are cancellable.

Accounts are explicit local credential-store names. `--account NAME` selects
`accounts/NAME.json` beside the default credential store. Names accept bounded
ASCII letters, digits, underscores, and hyphens; they cannot be paths. The existing
private-file store enforces directory/file permissions and rejects symlinks.
`account list`, `account show NAME`, and `account remove NAME` manage local records.
Inspection emits identity and granted scopes, never access or refresh tokens.
`login --account NAME` saves to that record.

Without explicit selection, existing environment/config/default-store precedence
is preserved. With explicit selection, saved login, client ID, access token, and
refresh token determine the account, overriding default token credentials. A
missing named record fails instead of falling back. Client secrets are still
supplied separately; they are not persisted in account files. `profile` continues
to mean a theme. Local historical reads do not need network credentials.

`chat --history PATH` attaches read-only to a local recorder database, replays a
bounded per-channel tail, and follows local sequences. It does not start a daemon
or claim complete coverage. The source has no send capability. `--allow-send`
adds a separately authenticated Helix sender through the same producer use case.
EventSub terminal readers also default to read-only, while the compatibility IRC
source retains its unconfirmed enqueue capability. `--allow-send` can select
acknowledged Helix sends for either live transport. The Helix sender currently
rejects `/me` actions explicitly; native IRC actions remain available.

`chat --record-history PATH` opens an explicit foreground recording sink. Capture
starts only after native channel readiness, stops on transport loss, and records
known upstream loss separately. It captures observed events before display queue
pressure or filters; local echoes never enter received history. Final durable
flush failure produces exit code 7. Startup channel selection defines this
capture; channels added later for display are excluded from that selection.

## Consequences and verification

Terminal readers can attach without advertising an unsupported sender. The
read-only composer preserves drafts and reports unavailable sends. History
replay refreshes moderation state before emission and follows moderation events.
The UI displays recorder failures separately from Twitch connection health.

Tests cover parallel dependency overrides, explicit-account precedence, missing
accounts, private files and symlinks, session cancellation/close ownership,
producer acknowledgement/retry boundaries, read-only replay/follow/moderation,
recording before display overflow, and shared-runtime manual replacement.
