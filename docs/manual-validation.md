# Manual Validation Evidence

This file records release-candidate manual evidence for environment-dependent
terminal behavior. It intentionally avoids screenshots, terminal recordings,
debug-log contents, and credential values.

## 2026-07-06 Ghostty Image Smoke Probe

Environment:

- Validation terminal: Ghostty-compatible graphics terminal.
- Visual inline-image rendering: confirmed by manual user report.

Command:

```sh
go run ./cmd/twi image-smoke --force
```

Result:

- The image smoke probe rendered visibly in Ghostty after the Kitty graphics
  command stopped using `C=1` cursor suppression and stopped printing trailing
  width-reserving spaces after the inline image payload.

## 2026-07-05 Image Smoke Probe

Environment:

- Active validation terminal: PTY with no Kitty/Ghostty graphics signal.
- Visual inline-image rendering: not claimed in this environment.

Commands run:

```sh
go run ./cmd/twi image-smoke
env XDG_CACHE_HOME=/tmp/twi-image-smoke-cache go run ./cmd/twi image-smoke --force
```

Results:

- `twi image-smoke` refused to claim graphics support and printed guidance to
  rerun with `--force` only in a known Kitty/Ghostty-compatible terminal.
- `twi image-smoke --force` with an isolated writable cache generated a local
  PNG, prepared it through the existing PNG image preparer, and emitted a Kitty
  graphics escape sequence with inline PNG payload bytes. The active PTY
  printed the sequence as terminal text, so visual image drawing remains
  unverified until the command is run inside a compatible graphics terminal
  session.

## 2026-07-04 T004 Terminal Matrix

Environment:

- Host command environment: Linux amd64, Go `go1.26.4`.
- Active validation terminal: PTY with `TERM=xterm-256color` and
  `COLORTERM=truecolor`.
- Terminal program/version: no `TERM_PROGRAM` or
  `TERM_PROGRAM_VERSION` value was present in the validation environment.
- Installed terminal binaries found: `kitty 0.45.0` at `/usr/bin/kitty` and
  `Ghostty 1.3.1` at `/snap/bin/ghostty`.
- Active Kitty/Ghostty graphics capability: skipped. The active PTY had no
  `KITTY_WINDOW_ID`, `KITTY_LISTEN_ON`, or `GHOSTTY_RESOURCES_DIR` signal, so
  it was not a real Kitty or Ghostty graphics session.
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
  no Kitty/Ghostty signal, writable cache, unsupported image capability in
  auto mode, and active text fallbacks.
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
- Kitty/Ghostty inline image drawing: skipped because the active PTY was not a
  Kitty or Ghostty graphics session despite both binaries being installed.
  Non-Kitty fallback behavior was checked through `doctor` and mock chat.
- Real pointer/mouse gestures: not driven manually in this PTY-only
  validation. The app has model-level tests for mouse wheel, sidebar click,
  composer click, message click, and disabled-mouse behavior; this pass
  manually covered the equivalent keyboard workflows.
