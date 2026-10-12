# History batching, failure acceptance, and performance budget

Measured 2026-10-12 with Go 1.27.2 on Linux x86_64. Reproduce with:

```sh
go test -count=1 -v ./internal/history -run TestSlowConsumerWorkload
go test -race ./internal/history
scripts/history-disk-full-smoke.sh
go test ./internal/cli -run TestOutputBeforeCheckpointCrashReplaysDuplicate
```

The workload records 600 messages across two busy channels while a third channel
remains quiet but has explicit capture coverage. Ingestion uses 100-record
transactions and a two-batch producer queue. Historical output reads pages of
64 messages and deliberately sleeps 1 ms after each output message. Live writes
continue between pages; the reader releases each SQL transaction before sleeping.
The captured high-water boundary excludes those later records from the replay.

Observed baseline:

| Metric | Measured | Regression budget |
| --- | ---: | ---: |
| Ingestion throughput | 9,591 records/s | 600 records within 10 s |
| Heap growth across ingestion and replay | 1,059,336 bytes | 64 MiB |
| Maximum queued producer batches | 2 | 2 |
| Maximum replay page | 64 messages | 64 |
| WAL size at end of workload | 1,062,992 bytes | 16 MiB |
| Maximum page output latency | 68.32 ms | 2 s |

These deliberately broad CI budgets detect runaway buffering, pinned read
transactions, or stalled ingestion. Throughput depends on CPU, storage, and
instrumentation; these figures are local measurements rather than Twitch
capacity claims. `TestSlowConsumerWorkload` enforces every budget and reports
fresh measurements with `-v`. Race instrumentation has also passed these gates.

The foreground recorder commits at 100 messages or the next 100 ms tick, whichever
comes first, and flushes pending records before recording a transport gap and
before completing graceful shutdown. A process kill can lose the current batch;
restart marks the prior capture interval unclean rather than inventing continuity.

Failure acceptance exercises the actual SQLite engine and processes:

- `TestSQLiteDiskFullRollsBackBatch` restricts SQLite's page allocation, triggers
  real `SQLITE_FULL`, and checks the batch remains absent. This proves database
  exhaustion behavior.
- `TestIsolatedFilesystemFull`, run by `scripts/history-disk-full-smoke.sh`,
  fills a fresh private 8 MiB container tmpfs until the operating system returns
  `ENOSPC`. The nonroot, network-disabled container reached exhaustion after
  8,192,000 filler bytes. Append then returns `ErrStorageFull`, preserves the
  previous committed sequence, and records unclean capture after capacity is
  restored. The container tmpfs disappears at exit; the host disk is never filled.
- `TestCorruptDatabaseRejected` overwrites a closed database and checks startup
  rejects its contents.
- `TestProcessKillRecoveryAndInterruptedMigration` kills subprocesses holding an
  active writer and an unfinished DDL transaction. Restart releases stale writer
  ownership, preserves committed records, marks capture unclean, and rolls back
  the incomplete schema change.
- `TestOutputBeforeCheckpointCrashReplaysDuplicate` exits a consumer subprocess
  after writing its first output record, before checkpoint completion. Restart
  repeats that record and then persists the completed checkpoint.
- `TestPruneBetweenPageAndEmission` removes a row after paging but before emission;
  refresh reports an expired cursor instead of emitting a stale buffered body.

# Explicit search and interactive capture

Schema 6 adds an FTS5 Unicode phrase index for `message` event bodies.
User notifications and moderation controls remain available through historical
consumption with `--events all`; they are outside this search index.
Search accepts a literal phrase,
returns newest retained messages first, and caps results at 1,000. SQL and FTS
operators in the input remain literal. Transactional triggers remove moderated
or pruned bodies from the searchable index. Existing stores migrate atomically
when explicitly opened by a recorder or prune command.

```sh
twi history search --history /path/to/history.sqlite3 --query 'hello chat' --limit 100
```

Search only inspects locally captured records and does not assert complete
historical coverage. Stores older than schema 6 require an explicit writer upgrade before read-only
use. `twi history upgrade --history PATH` performs that offline upgrade without
starting a recorder or changing retention policy. Read-only schema-6 stores apply
logical privacy protection immediately; an explicit writer persists repairs as
schema 7. The schema-6 FTS index remains message-event-only.

`history.OpenAttachment` supplies an explicit interactive recording port.
`Ready` starts capture only after confirmed channel readiness; `Lost` ends
coverage on disconnect. `Submit` has a bounded 100-record queue; `Errors` reports failures; `Close`
flushes and records capture completion. Queue exhaustion stops capture and marks
it unclean. `AddGap` lets the interactive transport report disconnected intervals.
This API requires an explicit path and channel selection and creates no daemon.

# Schema 7 legacy privacy repair

Schema 7 atomically reconciles retained text against deletion tombstones and
user/channel moderation boundaries. It also derives missing user boundaries
from already stored `user_messages_cleared` controls, which older dispatch code
persisted without applying them. Messages, notifications, system-text aliases,
notice parameters, fragments, and reply previews are protected. Store UUID,
sequences, and cursor positions remain unchanged.

Early notification records sometimes used a subtype such as `sub`, or an empty
ID, instead of the original message UUID. Matching a later deletion precisely is
impossible for those rows. If a channel deletion overlaps that older notification,
repair conservatively clears its body. Unmatched reply previews are also cleared
when they could refer to an ambiguous removed notification. A late notification
with the same author and old timestamp cannot restore that content, even with a
missing or newly recovered UUID. Later notifications remain readable.

Read-only schema-6 replay, refresh, search results, and tail selection apply the
same logical protection without modifying the database. Actual writer upgrade
persists body removal and refreshes FTS transactionally. Older unsupported schemas
fail with explicit upgrade guidance rather than leaking content or reporting raw
missing-column errors. `OpenExistingWriter` never creates a missing store or its
parent directory and refuses to initialize an empty file.
