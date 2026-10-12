# PLAN completion work

User requested all previously missing implementation and fixes, including UI v2.
User explicitly prohibited third-party history providers: Twitch is the only
network message source. Local recording/search remains supported. User authorized
live read/send/moderation validation against account/channel `worxbend`.
Credentials live only in private Git-ignored `.env`; access-token refresh succeeded.
Current granted capabilities: channel:bot, user:bot, user:read:chat, user:write:chat.
IRC read and moderator actions may require additional authorization.

Ownership:
- Architecture implementor: CLI dependency injection/use-case extraction, shared
  lifecycle, config/domain boundaries, named accounts, TUI history/capture wiring.
- UI implementor: Bubble Tea/Lip Gloss v2, cohesive controllers, native input/view,
  UI recorder status, full PTY matrix and regression tests.
- Storage implementor: bounded batches, FTS, actual process/storage faults,
  measured quiet/busy/slow-sink workload with numeric budgets, attachment API.
- Primary agent: typed HTTP/rate retry, EventSub parity, release hardening,
  packaging/live validation, integration/reviews/docs.

Work is incremental. Peer reviewers/judges verify other owners after implementation.
No automatic messages outside controlled Twitch tests. No publishing or commits.
