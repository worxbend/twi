# Manual Validation Evidence

This file records release-candidate manual evidence for environment-dependent
terminal behavior. It intentionally avoids screenshots, terminal recordings,
debug-log contents, and credential values.

## 2026-07-17 TUI Surface Refresh

Environment:

- Host command environment: Linux amd64, Go `go1.26.4`.
- Active validation terminal: default interactive PTY at `80x24`; terminal
  program/version was not reported by the environment.
- Twitch mode: deterministic `--mock`; no live chat connection was attempted.

Commands run:

```sh
go test ./...
go vet ./...
go test -race ./internal/app ./internal/render ./internal/theme
go build -o /tmp/twi-message-groups ./cmd/twi
env TERM=xterm-256color COLORTERM=truecolor /tmp/twi-message-groups chat --mock --channel alpha
git diff --check
```

Results:

- Formatting, full tests, vet, focused race tests, build, and whitespace checks
  passed.
- The splash animation transitioned to the dashboard without layout jitter.
- The dashboard showed the configured account and `#alpha` in the top bar; a
  darker full-terminal canvas; raised Chat and Emotes surfaces with icon titles
  and colored left rails; the inset composer with its channel/state footer; and
  the icon-bearing help strip.
- Mock authors retained different stable nickname colors while messages
  revealed. Consecutive messages from one author retained a shared group
  surface/rail, and visible author changes rendered a subtle horizontal rule.
  Automated tests cover grouping identity, separator rows, scrolling, mouse
  targeting, and contrast against both alternating group surfaces.
- `ctrl+c` exited cleanly and restored the terminal background with OSC 111.

## 2026-07-04 T004 Terminal Matrix

Environment:

- Host command environment: Linux amd64, Go `go1.26.4`.
- Active validation terminal: PTY with `TERM=xterm-256color` and
  `COLORTERM=truecolor`.
- Terminal program/version: no `TERM_PROGRAM` or
  `TERM_PROGRAM_VERSION` value was present in the validation environment.
- Viewports checked: default PTY `80x24`, wide PTY `100x30`, narrow PTY
  `48x14`, and an in-session resize from `100x30` to `48x14` and back to
  `100x30` using `SIGWINCH`.
- Twitch credential availability: skipped for live credentialed checks. A
  name-only environment probe showed `TWITCH_ACCESS_TOKEN`,
  `TWITCH_REFRESH_TOKEN`, `TWITCH_CLIENT_ID`, and `TWITCH_CLIENT_SECRET` were
  present, but both `TWI_TWITCH_USERNAME` and `TWITCH_USERNAME` were missing.
  Credential-free commands below explicitly emptied all Twitch credential
  variables and used isolated `/tmp/twi-agent-loop-*` config/cache paths.

Credential-free commands run:

```sh
mkdir -p /tmp/twi-agent-loop-config /tmp/twi-agent-loop-cache /tmp/twi-staticcheck-cache
env GOTOOLCHAIN=auto TERM=xterm-256color go version
env GOTOOLCHAIN=auto TERM=xterm-256color go mod tidy
env GOTOOLCHAIN=auto TERM=xterm-256color go fmt ./...
git diff --exit-code
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go vet ./...
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go test ./...
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go test -race ./...
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go tool govulncheck ./...
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= STATICCHECK_CACHE=/tmp/twi-staticcheck-cache go tool staticcheck ./...
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go build -o /tmp/twi-validation ./cmd/twi
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go run ./cmd/twi --help
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go run ./cmd/twi chat --mock --channel example
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go run ./cmd/twi chat --mock --channel one --channel two
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go run ./cmd/twi doctor
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go run ./cmd/twi config show
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config XDG_CACHE_HOME=/tmp/twi-agent-loop-cache TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go run ./cmd/twi setup --non-interactive --username example --channel example --login-dry-run
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config-live-empty XDG_CACHE_HOME=/tmp/twi-agent-loop-cache-live-empty TWI_TWITCH_USERNAME= TWI_TWITCH_OAUTH_TOKEN= TWI_TWITCH_REFRESH_TOKEN= TWI_TWITCH_CLIENT_ID= TWI_TWITCH_CLIENT_SECRET= TWITCH_USERNAME= TWITCH_ACCESS_TOKEN= TWITCH_REFRESH_TOKEN= TWITCH_CLIENT_ID= TWITCH_CLIENT_SECRET= go run ./cmd/twi chat --channel example
env GOTOOLCHAIN=auto TERM=xterm-256color XDG_CONFIG_HOME=/tmp/twi-agent-loop-config-debug XDG_CACHE_HOME=/tmp/twi-agent-loop-cache-debug TWI_TWITCH_USERNAME=<fake-marker> TWI_TWITCH_OAUTH_TOKEN=<fake-marker> TWI_TWITCH_REFRESH_TOKEN=<fake-marker> TWI_TWITCH_CLIENT_ID=<fake-marker> TWI_TWITCH_CLIENT_SECRET=<fake-marker> TWITCH_USERNAME=<fake-marker> TWITCH_ACCESS_TOKEN=<fake-marker> TWITCH_REFRESH_TOKEN=<fake-marker> TWITCH_CLIENT_ID=<fake-marker> TWITCH_CLIENT_SECRET=<fake-marker> go run ./cmd/twi chat --mock --channel example --debug-log --debug-log-path /tmp/twi-t004-debug.log
git diff --check
```

Results:

- Go hygiene, vet, tests, race tests, `govulncheck`, `staticcheck`, build, and
  credential-free CLI smokes passed. `govulncheck` reported no
  vulnerabilities.
- `twi doctor` reported missing credentials in the isolated environment,
  reachable Twitch IRC, `TERM=xterm-256color`, true-color via `COLORTERM`,
  and a writable cache. Avatars, badges, emotes, and emoji always render as
  text; there is no image capability check to report.
- `twi chat --channel example` with all credential variables empty failed
  safely with guidance for username/token variables, `--mock`, and
  `chat:read`/`chat:edit` scopes. No live network chat session was attempted.
- The mock debug-log smoke wrote `/tmp/twi-t004-debug.log` with file mode
  `0600`. A marker scan for the fake access token, refresh token, client
  secret, alias token values, callback-code markers, and bearer markers found
  no matches.
- The interactive PTY debug-log smoke wrote `/tmp/twi-t004-pty-debug.log` with
  file mode `0600`. A generic marker scan for OAuth/access-token,
  refresh-token, client-secret, callback-code, and bearer markers found no
  matches.

Interactive PTY checks:

- Default PTY `80x24`: `/tmp/twi-validation chat --mock --channel default80`.
  Verified initial mock layout, visible composer/help line, typed reveal
  animation, and clean `q` exit.
- Wide PTY `100x30`: `/tmp/twi-validation chat --mock --channel alpha --channel beta --debug-log --debug-log-path /tmp/twi-t004-pty-debug.log`.
- Verified mock chat starts with no network or credentials, sidebar renders two
  channels, typed-in reveal animation advances while the composer remains
  visible, `]` and `[` switch active channels, `tab` switches chat/composer
  focus, `1` toggles the mentions filter with status/sidebar feedback, `0`
  resets filters, `up` selects a message, `i` opens the inspect panel, `esc`
  closes/cancels panel state, composer reply context is shown for the selected
  message, entering a reply in mock mode fails safely with
  "send unavailable for this chat source", expanded help is reachable through
  the command palette path, and `q` exits cleanly.
- Narrow PTY `48x14`: `/tmp/twi-validation chat --mock --channel narrow`.
  Verified compact/no-sidebar layout, wrapped/truncated chat content,
  visible composer, ongoing reveal animation, `ctrl+r` feedback as
  "manual reconnect unavailable" for the mock source, and clean `q` exit.
- Live resize PTY: started `/tmp/twi-validation chat --mock --channel resize`
  at `100x30`, found the foreground process on its PTY, ran
  `stty rows 14 cols 48` on that PTY and sent `SIGWINCH`, then restored
  `100x30` and sent `SIGWINCH` again. Verified the same running process
  recomputed from wide layout to compact `48x14` layout and back to wide
  layout without dropping the composer, help line, or active mock messages.

Skipped or environment-limited checks:

- Credentialed Twitch read/send/reconnect: skipped because a complete
  credential set was not available. The ambient environment had no username,
  and no credential values were printed or used in credential-free smokes.
- Real pointer/mouse gestures: not driven manually in this PTY-only
  validation. The app has model-level tests for mouse wheel, sidebar click,
  composer click, message click, and disabled-mouse behavior; this pass
  manually covered the equivalent keyboard workflows.

## 2026-10-12 automation implementation

Credential-free checks used isolated temporary config/cache/state directories and
removed Twitch credential variables from child environments. No live chat was
sent or recorded.

- Ten native process cases passed: version/completion, producer JSONL dry-run and
  mixed outcomes, literal mode-alias message text, missing history, command help,
  conflicting root aliases, and multichannel mock TUI.
- Blocked-input producer SIGINT/SIGTERM returned 130/143 within five seconds.
  An undrained output pipe terminated within three seconds on deadline/SIGTERM.
- Linux PTY with `TERM=xterm-256color`: mock channels one/two launched at110x30,
  resized to80x24 through SIGWINCH, accepted theme-picker keys, and exited143 on
  SIGTERM. Alternate-screen transitions appeared in captured output. No terminal
  emulator product/version was available; this is PTY behavior, not a visual
  cross-emulator regression claim.
- Docker Engine29.9.0 built the nonroot image. Network-disabled, read-only
  container dry-run production, help, doctor, and two-channel mock smokes passed
  with private temporary config/cache/state mounts. Doctor correctly reported
  unavailable network/credentials.
- CGO-disabled binaries built for Linux and Darwin amd64/arm64. Existing
  Snap manifest remains; a new Snap package/publish run was not performed.
- Credentialed self-message, live moderation/shared-chat parity, real token
  rotation across Twitch sessions, and EventSub live subscription costs remain
  controlled-channel release checks. Scripted HTTP/WebSocket and local
  multiprocess quota tests cover those implementation boundaries offline.

## 2026-10-12 final PLAN completion evidence

This later pass supersedes the earlier implementation snapshot above. Native
Bubble Tea v2 PTY checks passed at 80x24, 32x12, and 120x30, including paste,
Unicode, focus, mouse protocol, resize, and terminal restoration on q, Ctrl+C,
and SIGTERM. Final release checks include four static targets, CycloneDX SBOMs,
unsigned provenance, nonroot Docker recorder signals/permissions, isolated real
ENOSPC, and an actual Snap package with payload/metadata smoke checks.

Controlled live EventSub tests on the authorized `worxbend` channel passed:
readiness, acknowledged send/reply, 500 Unicode scalar values, same-account
recording, reply identity, FTS search, SIGTERM143, and clean capture closure.
EventSub TUI launch/input/restoration also passed all three PTY exit paths
with the existing read grant and no composer sends. Three marked test messages
were sent by the separate acknowledged producer suite. Their deletion and IRC live validation
require the additional official OAuth grant; browser consent is unavailable in
this runtime. Installed Snap launcher testing requires interactive host sudo.
See [automation validation](automation-validation.md) and
[release evidence](release.md) for commands and exact verification boundaries.

## 2026-10-12 follow-up unfinished-work closure

The additional device grant saved a validated `worxbend` account privately. Both
IRC and EventSub real suites passed sending/replies/Unicode500, recording,
exact-ID deletion, FTS and persisted-body redaction, SIGTERM143 and clean capture
boundaries. EventSub also observed enhanced moderation actor identity. Earlier
three messages and nine new messages were deleted204; no test messages remain
from these suites. Private curated reports are in `/tmp/twi-live-eventsub-moderation.json`,
`/tmp/twi-live-irc-moderation.json`, and `/tmp/twi-live-send-modes.json`.

Real EventSub subscriptions recovered after forced socket closure with a capture
gap and bounded cancellation (`/tmp/twi-live-reconnect-reviewed.json`). Two real
recorders recovered the same invalid access token with one persisted OAuth
rotation, validated identity, and clean shutdowns (`/tmp/twi-live-auth-rotation.json`).
The rotated credential pair remains in the private named account.

App source-only and explicit-all sends were exercised in a verified standalone
room. Native IRC actions reported unconfirmed writes and were observed as
normalized action messages by the separate EventSub recorder. The user explicitly
skipped multi-room shared-chat validation; standalone delivery does not establish
propagation across rooms.

Installed Snap launcher checks passed in a checksum-verified Ubuntu24 KVM guest
with actual snapd installation, launcher checks, and removal. The host remained
unchanged. Supervisor examples use explicit systemd/launchd/Compose templates;
parsers passed and no service was installed. See [operations](operations.md),
[TUI profiles](tui-performance.md), and [hosted validation](hosted-validation.md).
