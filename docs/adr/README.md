# Architecture Decision Records

Each ADR records one decision, its context, and its consequences at the time
it was made. They are historical documents: later changes are recorded by
adding a note here or writing a new ADR, not by editing an old one into
agreement with the current code.

## Verifying an ADR against the code

Two things in this directory cannot be taken at face value.

**`PLAN.md` is not in the repository.** ADRs 0001, 0003, 0004, 0005, and 0006
cite it as the source of a requirement. It was an untracked working document
(`.gitignore` line 43) and was never published, so those citations are
historical context rather than references a contributor can follow. Treat the
stated requirement as the ADR author's summary of it.

**Some ADRs describe designs that were never built.** Check the code before
relying on an ADR's specifics. `go list -deps ./internal/<pkg>` settles any
question about which packages actually depend on which.

## Status notes

- **0002 — Wrap Helix identity and asset APIs.** The decision as written
  proposes a single `IdentityAssetClient` interface, possibly backed by
  `github.com/nicklaw5/helix/v2`. Neither exists. `internal/twitch` instead
  defines ten narrow, separately-faked interfaces (`StreamLookup`,
  `GameLookup`, `ChannelManager`, `ClipManager`, `MarkerManager`,
  `FollowerLookup`, `SubscriptionLookup`, `FollowedChannelLookup`,
  `UserLookup`, `ChatAssetLookup`), each with an injectable endpoint and HTTP
  client, over a hand-rolled Helix layer with no third-party Helix
  dependency. The shipped design is the better one — it keeps consumers
  depending only on the calls they make, and every client is testable against
  `httptest` with no network — but it was never written up. The ADR's
  *intent* (wrap Helix behind app-owned interfaces rather than leaking a
  vendor client into the app) holds; its *mechanism* does not.

- **0003 — Use Kitty graphics as the first image protocol.** Superseded.
  Terminal-graphics image rendering was removed in commit `22f5af9`; assets
  render as text, initials, and Unicode fallbacks only.
