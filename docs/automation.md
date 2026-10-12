# Automation and recorded history

`twi chat` retains the interactive interface. No arguments print help. Automation
requires an explicit command and channels; saved TUI channels are never selected
implicitly. `-P` and `-C` alias `produce` and `consume`.

```sh
twi login --capability chat-read
twi consume -t alpha --duration 10m

twi login --capability chat-send
printf '%s\n' 'Hello chat' | twi produce -t alpha
twi produce -t alpha -m 'First' -m 'Second' --dry-run

# Run before the history interval you need, in another terminal.
twi record -t alpha --retention 168h --max-bytes 1GiB
twi consume -t alpha --last 10
twi consume -t alpha --since 10m
twi consume -t alpha --last 10 --follow
twi consume -t alpha --offset beginning --follow --cursor-file ./alpha.cursor
twi history status
twi history upgrade --history /path/to/existing/history.sqlite3
twi history prune --dry-run
```

Twitch does not provide arbitrary earlier chat through these adapters. A recorder
must already have observed the requested interval. History requests fail with
exit 6 when capture coverage is incomplete. `--allow-partial` explicitly accepts
known gaps with stderr warnings. Relative times resolve once at command start.
`--last N` selects N messages per channel; `--count N` bounds total output.
History selections are finite by default; `--follow` tails the same database.
Live reads follow by default. `--duration` limits lifetime, not historical age.

Output defaults to LF-delimited JSON objects, schema version 1. Diagnostics go to
stderr, optionally `--diagnostic-format jsonl`. `--format text` escapes line breaks
and removes terminal controls/bidi formatting. `--text-only` loses identity.
No raw tags or credentials enter public records. See [schemas](schemas/README.md).

Producer lines preserve spaces and accept CRLF/final unterminated lines. Empty
records fail unless `--skip-empty`. JSONL input accepts only `channel`, `text`,
`reply_to_message_id`, and `client_id`; supplied channel flags form an allowlist.
Multiple line destinations require `--broadcast`. A batch is not transactional:
a malformed later file/stdin record cannot undo earlier sends. Finite `-m` input
is validated before sending. Input never executes slash commands or shell code.

A `sent` receipt means Helix acknowledged `is_sent=true`. HTTP 200 alone does not
mean sent. Timeouts can yield `unknown`; they are never automatically retried.
`client_id` correlates receipts and is not a Twitch idempotency key. Shared chat
may propagate user-token sends beyond the selected observed channel. Dry runs
return `validated` and need no credentials. Normal-account quotas are reserved
in a private account journal shared by local twi processes; other hosts/apps can
still consume the same quota.

The foreground recorder writes no data stdout. Its SQLite store defaults to
`$XDG_STATE_HOME/twi/history.sqlite3`, or `~/.local/state/twi/history.sqlite3`.
Use `--history PATH` to override. One writer and multiple local readers use WAL
and FULL synchronous commits. Network filesystems are unsupported. Directories
are private (0700), files/checkpoints/locks private (0600); unsafe paths fail.
Defaults retain 168 hours within a 1 GiB target including WAL. Maintenance has
bounded headroom; this is not a byte-exact filesystem ceiling.

Capture heartbeats distinguish quiet channels from stopped capture. Disconnections,
queue loss, and unclean restarts produce coverage failures. Coverage means no
known observation gaps; event-time completeness cannot be proven when late
messages arrive. Recorders only store observed messages, never TUI local echoes.
IRC self-message observation and credentialed shared-chat/moderation parity need
controlled-channel validation. Use explicit `--transport eventsub` with
`eventsub-read` capability for the newer adapter; IRC remains the default.

Moderation removes retained message bodies/fragments/reply previews. Logical
redaction cannot retract previous stdout, backups, or prove physical secure erase.
Back up a stopped recorder by copying its database after a clean shutdown. For a
live backup use SQLite's backup API/tool; copying only the main file during WAL
activity can lose committed data. Restore with private modes and preserve the
store UUID. Newer schema versions fail clearly on older binaries.

Cursor files save store identity, scan position, selection fingerprint, and resolved
time bounds. First use requires a selector. Changed selections/different stores/
pruned positions fail. Checkpoints advance after successful output. A crash after
output but before checkpoint may repeat a record; downstream durability is not
proved by a pipe write. Deduplicate by message identity/cursor when required.

Exit codes: 0 normal bounded read/all sends accepted or validated; 1 internal;
2 arguments/input; 3 authorization; 4 network or unknown send; 5 rejection;
6 history/cursor/coverage; 7 local I/O/storage; 8 mixed send batch; 130 SIGINT;
143 SIGTERM. Consumer broken pipes succeed; producer broken pipes stop sends
and fail with 7 because receipts cannot be reported reliably.

Discover commands with `twi --help`, command `--help`, `twi version`, and
`twi completion bash|zsh|fish`. Credentials use the existing private store or
configuration/environment precedence. No data command opens browser login.

Reader capabilities differ:

| Capability | IRC | EventSub |
| --- | --- | --- |
| Message, reply, fragments | Supported normalized records | Supported normalized records |
| Delete, channel/user clear | Supported | Four selected chat subscriptions cover these |
| User notices/membership/room state | Supported when Twitch supplies them | Not subscribed; not promised by `--events all` |
| Shared source identity/delivery ID | Limited IRC tags | Explicit source fields and delivery identity |
| Historical network replay | None | None |

The EventSub implementation checks returned subscription cost/quota rather than
assuming a fixed per-channel cost. Scope/lifecycle reference:
[chat subscription types](https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/)
and [WebSocket handling](https://dev.twitch.tv/docs/eventsub/handling-websocket-events/).

Implementation scope and evidence: [validation](automation-validation.md).

Local IRC authentication and join attempts share conservative 20-per-10-second
permit journals. IRC and EventSub share a conservative 100-distinct-room account
lease limit. Multiple local clients of the same room share that room reservation;
other hosts/apps remain outside local coordination. Large channel sets may need
a larger `--connect-timeout` because channel additions are paced.

## Additional CLI features

`history search --query PHRASE --history PATH [-t CHANNEL]` searches a literal
phrase through private captured message bodies. Results are bounded and respect
moderation and retention; it never searches a remote provider.

`login --account NAME` stores a separate private account. `account list`,
`account show NAME`, and `account remove NAME` inspect/manage those records
without printing tokens. Explicit `--account NAME` selects that store and never
falls back to a default account's token. `profile` continues to mean a theme.

TUI options: `chat --history PATH` replays/follows an existing local store and is
read-only by default. `--allow-send` separately enables acknowledged Helix sends.
`chat --record-history PATH` explicitly records observed startup-channel events
in the foreground; UI filters/local echoes cannot become history. Runtime-added
display channels are not automatically recorded. `--transport eventsub` uses the
same shared reader engine as headless commands; IRC remains the default.

Normal destinations keep 20 messages/30s and 1 message/s/channel. Proven own
broadcaster/moderator/VIP roles may use 100 messages/30s, with explicit user pacing
still enforced. Every send counts toward both account windows. Roles never come
from display names or a user-provided override.

EventSub now subscribes to seven read-scoped chat types per channel; the local
ceiling is 42 channels within 300 subscriptions. Full moderation is added only
when both the necessary scopes and a verified moderator/broadcaster role exist.
Actual subscription costs from Twitch are checked. Unsupported membership and
room-state semantics are not invented.

Private environment helper: `python3 scripts/run-with-env.py --env-file .env
COMMAND ...` accepts literal assignments only, rejects unsafe files, and never
evaluates shell code or prints credential values. Live checks are opt-in through
`scripts/live-validation.py`; see [implementation evidence](automation-validation.md).

## Explicit offline history upgrade

Stop the recorder holding the database writer lease, then run:

```sh
twi history upgrade --history /path/to/existing/history.sqlite3
```

This command requires an existing private database. It does not create a missing
file or parent directory, start network recording, prune history, or change the
retention policy. It performs supported schema migrations and returns the normal
`history_status` JSON with `database_schema`. Store UUID, sequence numbers, and
cursor positions remain stable. Cancellation interrupts migration and rolls
back the active transaction. Keep a consistent backup before upgrading valuable
stores; use a consistent SQLite backup after stopping the writer.

Schema7 repairs previously removed notice/message bodies and reply previews,
including older user-clear controls that did not apply their privacy effects.
Legacy notice identities that cannot be matched reliably are redacted
conservatively; suppression persists for delayed copies. Schema6 read-only
queries apply logical privacy overlays without changing the file. Older schemas
require an explicit writer upgrade; `history prune --dry-run` stays read-only
and cannot upgrade them. Physical remnants in old WAL pages/backups are outside
the logical redaction guarantee.

Create a consistent private backup with Python's SQLite backup API, including
committed data in the source WAL. Stop the writer and attached readers first;
choose a fresh destination inside a private directory:

```sh
umask 077
python3 - /path/to/history.sqlite3 /path/to/private/history-backup.sqlite3 <<'PY'
import os, pathlib, sqlite3, sys, urllib.parse
source = pathlib.Path(sys.argv[1]).resolve(strict=True)
destination = pathlib.Path(sys.argv[2])
fd = os.open(destination, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
os.close(fd)
with sqlite3.connect('file:' + urllib.parse.quote(str(source)) + '?mode=ro', uri=True) as reader:
    with sqlite3.connect(destination) as backup:
        reader.backup(backup)
PY
```

To recover, stop all processes using the store, preserve the failed database
and any matching `-wal`/`-shm` files together, and restore the consistent backup
at the original database path with mode0600. Move the failed sidecars away from
that original path before opening the restored database. Keep its directory at
0700. A restored pre-upgrade store can then be upgraded explicitly again. Do
not replace an active recorder's files or copy only its main database while its
WAL is changing. Backups retain their own historical bodies and need the same
private storage and retention controls.
