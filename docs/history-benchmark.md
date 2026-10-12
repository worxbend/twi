# Local history workload baseline

Measured 2026-10-12 with Go 1.27.2 on Linux/amd64, AMD Ryzen 7 5700X,
using modernc.org/sqlite v1.60.1 (embedded SQLite 3.53.4).

Command:

```sh
go test ./internal/history -run '^$' -bench BenchmarkRecordedWorkload -benchtime=10000x -count=1 -benchmem
```

The benchmark commits 10,000 distinct observed messages across three channels,
with SQLite WAL and synchronous FULL. It then replays the fixed snapshot through
a separate reader in pages of at most 256 records. Between pages the writer
commits new records; those records must not enter the original snapshot.

Observed result:

```text
10000  105423 ns/op  256.0 max-page  42.95 replay-ms  6982072 store-bytes  4196 B/op  81 allocs/op
```

This measures approximately 9,486 single-record durable appends per second on
this local test filesystem. Replay completed in 42.95 ms, including the additional
writes between pages. Total database/WAL/SHM footprint was 6,982,072 bytes.
Allocation figures describe the append loop, not peak process memory. The maximum
page contained 256 records; no reader keeps a SQLite transaction open while
processing a page.

This baseline does not simulate Twitch traffic, disk exhaustion, a network
filesystem, or a sustained slow downstream consumer. Filesystem and hardware
change these results. The recorder currently commits each event individually;
there is no batch-throughput claim. Retention uses at most 64 maintenance passes,
with 1,024 rows and 1,024 vacuum pages per pass, and a 4 MiB maintenance allowance
above the configured byte target. It returns a storage error if bounded work
cannot reduce the footprint. The byte setting is a target, not a hard filesystem
ceiling during active transactions.
