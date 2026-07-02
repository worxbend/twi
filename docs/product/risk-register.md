# Risk Register

Status: Risk register aligned with the current MVP. Mock chat is ready;
one-channel live IRC read/send and diagnostics are partial; login,
multi-channel live chat, and inline terminal images remain planned.

Credential assumption: Twitch username/token values currently come from
environment variables or the flat config file. CLI overrides cover channel and
config path, not username or token values.

Likelihood and impact use `Low`, `Medium`, or `High`.

| ID | Risk | Likelihood | Impact | Mitigation | Owner lane | Verification signal |
| --- | --- | --- | --- | --- | --- | --- |
| RR-001 | Twitch auth scopes or token ownership are wrong, causing read/send failures. | High | High | Require `chat:read` and `chat:edit` for IRC MVP; validate token when Helix is available; show actionable errors without printing secrets. | Twitch integration engineer | Invalid-token and missing-scope tests; manual login/config failure check; redaction tests. |
| RR-002 | Twitch rate limits or phone verification block sending. | Medium | High | Add local send queue, visible cooldown/status, clear failure messages, and preserve composer text on failure. | Twitch integration engineer | Fake sender rate-limit tests; manual failed-send scenario; no lost composer text. |
| RR-003 | Terminal image protocol behavior varies across Kitty, Ghostty, and non-Kitty terminals. | High | Medium | Capability-detect image support; keep image renderer behind interface; ship text/initial fallbacks first. | Asset/image engineer | `twi doctor` image signal later; manual Kitty/Ghostty/non-Kitty matrix; golden fallback snapshots. |
| RR-004 | Image cache or asset downloads slow chat rendering. | Medium | High | Never block Bubble Tea `Update` or `View`; async asset pipeline; bounded cache; placeholders reserve width. | Asset/image engineer | Tests with delayed asset fake; search for blocking I/O in `View`; stress run with cache misses. |
| RR-005 | Busy channels create an unbounded animation backlog or sluggish UI. | High | High | Bound reveal queue; coalesce/skip reveals under load; support reduced/off modes; render off-screen rows statically. | Motion engineer | Fake clock throughput tests; stress harness; queue length assertions. |
| RR-006 | Unicode, emoji, ANSI styles, or emote placeholders are corrupted by reveal/wrapping. | High | High | Build reveal units from normalized render fragments, not raw strings; use width-aware and grapheme-safe logic. | Rendering engineer and motion engineer | Golden partial-frame tests; grapheme tests; wide-character layout tests. |
| RR-007 | Secrets leak through config output, logs, errors, snapshots, or tests. | Medium | High | Central redaction helper; avoid raw config dumps; add secret fixtures to tests; review diagnostics before handoff. | QA/release engineer and Twitch integration engineer | Redaction unit tests; grep for fixture token values; `config show` excludes secrets. |
| RR-008 | UI accidentally couples directly to Twitch library types, making EventSub or tests difficult. | Medium | Medium | Define internal normalized messages and `ChatClient` before real IRC adapter; enforce package boundaries. | Core TUI engineer and Twitch integration engineer | Interface tests/fakes; code review: no Twitch library imports in `internal/app`. |
| RR-009 | Network callbacks or file/cache operations block the Bubble Tea loop. | Medium | High | Bridge callbacks through typed messages and commands; keep all network/cache work async; avoid I/O in `View`. | Core TUI engineer | Integration test with slow fake; code search for blocking calls in app update/view. |
| RR-010 | Config precedence or platform paths are inconsistent. | Medium | Medium | Implement flags > env > file > defaults; use platform config/cache dirs; test path and precedence rules. | Core TUI engineer | Config precedence unit tests; `config path` and `config show` checks. |
| RR-011 | Multi-channel support adds state bugs after one-channel MVP assumptions harden. | Medium | Medium | Shape channel state as a map/list from the start even if MVP joins one channel; isolate per-channel history/composer/connection state. | Core TUI engineer | Two-channel model tests before real multi-channel UI; later manual switch test. |
| RR-012 | Go toolchain drift causes inconsistent builds across agents. | Medium | Medium | Record verified stable version; pin `go` and `toolchain`; keep `GOTOOLCHAIN=auto` compatible; use module tool directives. | QA/release engineer | `go version`; `go env GOTOOLCHAIN`; clean `go mod tidy`; CI/tooling gate. |
| RR-013 | Twitch Helix/API outages or quota issues break avatars, badges, emotes, or token validation. | Medium | Medium | Treat asset/API data as optional; cache metadata; keep chat read/send usable without assets. | Twitch integration engineer and asset/image engineer | Fake API failure tests; fallback golden snapshots; manual degraded-mode status. |
| RR-014 | Visual design becomes a debug console or unreadable in small terminals. | Medium | Medium | Define layout snapshots; correct low-contrast username colors; hide sidebar/status details in narrow mode. | Core TUI engineer and rendering engineer | Golden layouts at narrow/normal/wide widths; manual resize check. |
| RR-015 | Interactive login flow is blocked by Twitch OAuth constraints. | Medium | Medium | Keep MVP token config path; research device-code/local-callback options before implementation; document fallback. | Twitch integration engineer | Auth ADR; manual token setup docs; login task blocked only if MVP token config works. |

## Highest Early Risks

Address these before rich polish work:

1. Secret handling in config/auth output.
2. Normalized message and render-fragment model.
3. Grapheme-safe typed-in animation.
4. Non-blocking Twitch and asset pipelines.
5. Terminal image fallback contract.
