# 0008: Local SQLite history with explicit capture coverage

Status: accepted.

Use CGO-free modernc SQLite, WAL, FULL synchronous mode, one OS-locked writer,
private state files, and bounded readers. Store observed normalized events,
identity deduplication, capture intervals/gaps, and monotonic sequence cursors.
Replay and follow share the database; no independent live handover or hidden daemon.
Retention and moderation preserve accurate cursor/coverage failures.

Consequences: prior chat requires earlier recording. Coverage is observation
coverage, never arbitrary Twitch backfill or guaranteed exactly-once delivery.
Checkpoints permit repeated output after crashes. Network filesystems are unsupported.
