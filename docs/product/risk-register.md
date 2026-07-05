# Risk Register

Status: Risk register aligned with the release-candidate MVP. Mock chat is
ready; diagnostics are ready; multi-channel live IRC read/send, multi-channel
UX, redacted debug logging, release binary/container packaging, and inline image
plumbing are partial or current for their documented paths; on supported Unix
builds login can save through the restrictive credential file fallback; setup
can write non-secret config and hand off to login; refreshed IRC tokens are
saved through the supported credential store when available. Credentialed
Twitch release validation, exact Docker CLI validation, and manual
Kitty/Ghostty image validation remain environment-dependent.

Credential assumption: Twitch username/token values currently come from
environment variables, the flat config file, or saved credentials on supported
platforms. Environment and flat config values take precedence over saved
credentials. CLI overrides cover channel and config path, not username or token
values.

Likelihood and impact use `Low`, `Medium`, or `High`.

| ID | Risk | Likelihood | Impact | Mitigation | Owner lane | Verification signal |
| --- | --- | --- | --- | --- | --- | --- |
| RR-001 | Twitch auth scopes or token ownership are wrong, causing read/send failures. | High | High | Require `chat:read` and `chat:edit` for IRC MVP; validate token when Helix is available; show actionable errors without printing secrets. | Twitch integration engineer | Invalid-token and missing-scope tests; manual login/config failure check; redaction tests. |
| RR-002 | Twitch rate limits or phone verification block sending. | Medium | High | Add local send queue, visible cooldown/status, clear failure messages, and preserve composer text on failure. | Twitch integration engineer | Fake sender rate-limit tests; manual failed-send scenario; no lost composer text. |
| RR-003 | Terminal image protocol behavior varies across Kitty, Ghostty, and non-Kitty terminals. | High | Medium | Capability-detect image support; keep image renderer behind interface; ship text/initial fallbacks first; do not claim Kitty/Ghostty drawing until manual evidence exists. | Asset/image engineer | `twi doctor` image signals; non-Kitty fallback manual evidence; Kitty/Ghostty image smoke still pending; golden fallback snapshots. |
| RR-004 | Image cache or asset downloads slow chat rendering. | Medium | High | Never block Bubble Tea `Update` or `View`; async asset pipeline; bounded cache; placeholders reserve width. | Asset/image engineer | Tests with delayed asset fake; search for blocking I/O in `View`; stress run with cache misses. |
| RR-005 | Busy channels create an unbounded animation backlog or sluggish UI. | High | High | Bound reveal queue; coalesce/skip reveals under load; support reduced/off modes; render off-screen rows statically. | Motion engineer | Fake clock throughput tests; stress harness; queue length assertions. |
| RR-006 | Unicode, emoji, ANSI styles, or emote placeholders are corrupted by reveal/wrapping. | High | High | Build reveal units from normalized render fragments, not raw strings; use width-aware and grapheme-safe logic. | Rendering engineer and motion engineer | Golden partial-frame tests; grapheme tests; wide-character layout tests. |
| RR-007 | Secrets leak through config output, logs, errors, snapshots, or tests. | Medium | High | Central redaction helper; avoid raw config dumps; add secret fixtures to tests; review diagnostics before handoff. | QA/release engineer and Twitch integration engineer | Redaction unit tests; grep for fixture token values; `config show` excludes secrets. |
| RR-008 | UI accidentally couples directly to Twitch library types, making EventSub or tests difficult. | Medium | Medium | Define internal normalized messages and `ChatClient` before real IRC adapter; enforce package boundaries. | Core TUI engineer and Twitch integration engineer | Interface tests/fakes; code review: no Twitch library imports in `internal/app`. |
| RR-009 | Network callbacks or file/cache operations block the Bubble Tea loop. | Medium | High | Bridge callbacks through typed messages and commands; keep all network/cache work async; avoid I/O in `View`. | Core TUI engineer | Integration test with slow fake; code search for blocking calls in app update/view. |
| RR-010 | Config precedence or platform paths are inconsistent. | Medium | Medium | Implement flags > env > file > defaults; use platform config/cache dirs; test path and precedence rules. | Core TUI engineer | Config precedence unit tests; `config path` and `config show` checks. |
| RR-011 | Multi-channel support adds state bugs after early single-channel assumptions harden. | Medium | Medium | Keep channel state as a map/list; isolate per-channel history/composer/connection state; document connection-level IRC callbacks. | Core TUI engineer | Two-channel model tests; fake multi-channel routing tests; later manual switch test. |
| RR-012 | Go toolchain drift causes inconsistent builds across agents. | Medium | Medium | Record verified stable version; pin `go` and `toolchain`; keep `GOTOOLCHAIN=auto` compatible; use module tool directives. | QA/release engineer | `go version`; `go env GOTOOLCHAIN`; clean `go mod tidy`; CI/tooling gate. |
| RR-013 | Twitch Helix/API outages or quota issues break avatars, badges, emotes, or token validation. | Medium | Medium | Treat asset/API data as optional; cache metadata; keep chat read/send usable without assets. | Twitch integration engineer and asset/image engineer | Fake API failure tests; fallback golden snapshots; manual degraded-mode status. |
| RR-014 | Visual design becomes a debug console or unreadable in small terminals. | Medium | Medium | Define layout snapshots; correct low-contrast username colors; hide sidebar/status details in narrow mode. | Core TUI engineer and rendering engineer | Golden layouts at narrow/normal/wide widths; manual resize check. |
| RR-015 | OAuth login UX is blocked by Twitch app registration or local callback constraints. | Medium | Medium | Keep MVP token config path; support a localhost callback flow with clear dry-run and fallback docs; document redirect URI requirements. | Twitch integration engineer | Login command tests; auth docs; manual token setup docs; redaction checkpoint. |
| RR-016 | Stored credentials are created through an unsafe platform backend. | Medium | High | Keep credential persistence behind `CredentialStore`; support the file fallback only on Unix builds; require exact `0700` credential directories, `0600` credential files, symlink rejection, and no-follow opens; keep non-Unix saved credentials disabled. | Twitch integration engineer and QA/release engineer | Storage permission unit tests; non-Unix gated tests; `config show`/`doctor` redaction checks; manual docs review. |
| RR-017 | Release artifacts are treated as fully published despite incomplete environment-specific validation. | Medium | Medium | Keep the release dry-run credential-free; separate Docker, credentialed Twitch, Kitty/Ghostty, registry, signing, notarization, and package-manager claims from the automated artifact build; document skipped environment checks explicitly. | QA/release engineer | Release dry-run artifacts and checksums; exact Docker CLI smokes in final validation; manual-validation evidence for credentialed and terminal-specific checks. |

## Highest Early Risks

Address these before rich polish work:

1. Secret handling in config/auth output.
2. Normalized message and render-fragment model.
3. Grapheme-safe typed-in animation.
4. Non-blocking Twitch and asset pipelines.
5. Terminal image fallback contract.
