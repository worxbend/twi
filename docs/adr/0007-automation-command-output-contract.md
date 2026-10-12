# 0007: Explicit automation commands and versioned output

Status: accepted.

Keep the interactive command and no-argument help. Add explicit producer,
consumer, recorder, and history commands. JSONL v1 DTOs are the default regardless
of terminal detection. Data stdout contains only records; diagnostics use stderr.
Use injected context/streams, strict input routing, typed receipts, and documented
exit codes. Text output reuses terminal safety protections.

Consequences: scripts can compose commands without terminal state. Output evolves
additively within schema v1; incompatible meaning changes require a new version.
