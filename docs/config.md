# Configuration

This document describes the configuration model for `twi`. The implemented parser is intentionally small and should be expanded deliberately as planned commands become real. For authentication details, read [auth.md](auth.md); for setup symptoms and fixes, read [troubleshooting.md](troubleshooting.md).

## Current State

- Config loading exists for flat `key = value` files, environment variables, and selected CLI overrides.
- `twi config show` and `twi config path` exist in the CLI.
- Mock chat is ready and does not require credentials or network access.
- Multi-channel live IRC read/send is partially shipped: `twi chat --channel <channel> [--channel other]` validates startup credentials when Twitch OAuth validation is reachable, then can read, send, reply, and send `/me` actions when username/token credentials are configured.
- Twitch credentials are currently read from environment variables, the flat config file, or saved credentials on supported Unix platforms. Unix builds use the private credential file. Non-Unix builds keep saved credentials disabled. CLI flags currently override `--config` and `--channel`, not username or OAuth token values.
- Config output redacts OAuth tokens and client secrets.
- `twi doctor` diagnostics report the effective config file path, credential
  presence, Twitch OAuth identity/expiry/scope validation, refresh availability,
  username mismatch, selected feature modes, Twitch IRC reachability, terminal
  hints, Kitty graphics signals, cache directory writability/pruning, image
  capability, and live image-stack readiness without printing token or client
  secret values.
- Redacted structured debug logging is opt-in through flat config, environment,
  or command flags for chat, login, and doctor. Debug records use curated fields
  for auth, network, asset, render, send, and connection diagnostics rather
  than raw transport structs or raw tag maps.
- Multi-channel UX is partially shipped: per-channel history, unread counts, scroll, drafts, replies, sends, local view filters, keyboard sidebar, command palette, optional mouse interactions, and selected-message inspect are current behavior.
- Inline terminal image support is partially shipped: bounded image decode/cell preparation, renderer cells, stable fallback rows, cache boundaries, standard emoji provider metadata, capability diagnostics, visible-row asset event scheduling, and default live resolver/downloader/preparer/renderer wiring exist; manual Kitty/Ghostty validation remains planned for a compatible graphics terminal session.
- `twi setup` can create or update non-secret flat config values and hand off
  to login. On supported Unix platforms, `twi login` can run the OAuth
  browser/callback flow and save returned tokens without printing them through
  the restrictive credential-file fallback. Non-Unix builds keep saved
  credentials disabled and users should use environment variables or a private
  flat config file.
- Nested TOML tables are not implemented yet; keep config files flat.

## Precedence

Effective config should be resolved in this order, highest priority first:

1. CLI flags for `--config`, `--channel`, `--debug-log`, and
   `--debug-log-path`.
2. Environment variables.
3. Config file.
4. Saved credential values for empty credential fields on supported Unix platforms.
5. Defaults.

This order lets users override local config for one command without editing files.
Empty environment variable values are ignored, which keeps isolated CI smokes
from masking config-file values with deliberately blank secret env vars.

## Setup Command

`twi setup` is the guided config path. It writes only non-secret flat config
keys:

- `twitch_username`
- `twitch_client_id`
- `default_channels`
- `enable_kitty_images`
- `enable_mouse`
- `image_mode`
- `avatar_mode`
- `emoji_mode`
- `emoji_provider`
- `emote_mode`
- `animation_mode`

It never asks for or writes OAuth tokens, refresh tokens, callback codes, OAuth
state, authorization URLs, or client secrets. Existing secret lines in
`config.toml` are preserved unchanged if the file already has them, but setup
does not create or update those secret keys.

Interactive use:

```sh
twi setup
```

Automation-friendly use:

```sh
twi setup --non-interactive --username my_login --channel somechannel --image-mode auto --emoji-provider twemoji --animation-mode fast
```

Credential handoff options:

```sh
twi setup --login
twi setup --login-dry-run
```

`--login` delegates to `twi login` after writing non-secret config. `--login-dry-run`
uses the login smoke path without opening a browser, starting a callback
listener, contacting Twitch, printing secrets, or writing credentials.

## Config Paths

Linux and macOS should follow XDG config rules:

```text
$XDG_CONFIG_HOME/twi/config.toml
~/.config/twi/config.toml
```

The cache directory is the platform cache directory, such as:

```text
$XDG_CACHE_HOME/twi
~/.cache/twi
```

Cache contents should include non-secret metadata and downloaded assets only, such as avatar, emote, badge, and emoji data.

On supported Unix builds, the credential fallback path is a separate private
file in the platform config directory:

```text
$XDG_CONFIG_HOME/twi/credentials.json
~/.config/twi/credentials.json
```

This fallback is not the flat `config.toml` file. On Unix builds, `twi`
creates the containing directory with `0700` permissions and the credential file
with `0600` permissions, rejects credential files or directories whose
permissions do not match those exact modes, rejects symlinks at the credential
directory or file path, and opens existing credential files with no-follow
protection. These guarantees are Unix file-mode and no-follow guarantees.

Non-Unix platforms keep saved credentials disabled. On those platforms,
use environment variables or a private flat config file for Twitch credentials.
`twi doctor` reports disabled saved credential persistence as a warning, and
`twi login` exits with a redacted actionable error before opening the browser.

## Environment Variables

Supported variables:

| Variable | Secret | Purpose |
| --- | --- | --- |
| `TWI_TWITCH_USERNAME` | No | Twitch login for IRC auth. |
| `TWI_TWITCH_OAUTH_TOKEN` | Yes | Twitch IRC OAuth token with `chat:read` for live reads and `chat:edit` for composer sends. |
| `TWI_TWITCH_REFRESH_TOKEN` | Yes | Refresh token used for one OAuth refresh after live IRC auth failure. Refreshed tokens are saved through the supported credential store when available; otherwise they stay in memory for the current process with a warning. |
| `TWI_TWITCH_CLIENT_ID` | Usually no | Twitch app client ID for Helix/API calls. |
| `TWI_TWITCH_CLIENT_SECRET` | Yes | Client secret used by `twi login` and in-memory OAuth refresh. |
| `TWI_TWITCH_REDIRECT_URL` | No | Default localhost OAuth callback URL for `twi login`, used when `--redirect-uri` is not passed explicitly. An explicit `--redirect-uri` flag still wins. |
| `TWITCH_USERNAME` | No | Dotenv alias for Twitch login. Canonical `TWI_TWITCH_USERNAME` wins if both are set. |
| `TWITCH_ACCESS_TOKEN` | Yes | Dotenv alias for OAuth token. A missing `oauth:` prefix is added for IRC use. Canonical `TWI_TWITCH_OAUTH_TOKEN` wins if both are set. |
| `TWITCH_REFRESH_TOKEN` | Yes | Dotenv alias for refresh token. Used for one OAuth refresh after live IRC auth failure. Refreshed tokens are saved through the supported credential store when available; otherwise they stay in memory for the current process with a warning. |
| `TWITCH_CLIENT_ID` | Usually no | Dotenv alias for client ID. |
| `TWITCH_CLIENT_SECRET` | Yes | Dotenv alias for client secret. |
| `TWITCH_REDIRECT_URL` | No | Dotenv alias for the OAuth callback URL. Canonical `TWI_TWITCH_REDIRECT_URL` wins if both are set. |
| `TWI_DEFAULT_CHANNELS` | No | Default channel list. |
| `TWI_ENABLE_KITTY_IMAGES` | No | Enable or disable Kitty protocol image support. |
| `TWI_ENABLE_MOUSE` | No | Enable or disable terminal mouse reporting and mouse shortcuts. |
| `TWI_IMAGE_MODE` | No | Overall image mode. |
| `TWI_AVATAR_MODE` | No | Avatar rendering mode. |
| `TWI_EMOJI_MODE` | No | Standard emoji rendering mode. |
| `TWI_EMOJI_PROVIDER` | No | Standard emoji image metadata provider. Defaults to `twemoji`. |
| `TWI_EMOJI_URL_TEMPLATE` | No | Custom public emoji image URL template required when `TWI_EMOJI_PROVIDER=custom`. Use `{id}` for the normalized emoji asset key. Credential markers are rejected by the provider and redacted from `config show`. |
| `TWI_EMOTE_MODE` | No | Twitch emote rendering mode. |
| `TWI_ANIMATION_MODE` | No | Animation behavior: pulsing status indicators, scene-switch flash, startup splash, and command-palette typewriter reveal, in addition to the existing chat-row reveal speed. |
| `TWI_THEME_NAME` | No | Active theme: one of the 13 built-in preset names, or `custom` to use the `TWI_THEME_*` hex fields below. Defaults to `claude`. |
| `TWI_THEME_BACKGROUND` / `TWI_THEME_FOREGROUND` / `TWI_THEME_ACCENT` / `TWI_THEME_MUTED` / `TWI_THEME_BORDER` / `TWI_THEME_SURFACE` / `TWI_THEME_WARNING` / `TWI_THEME_ERROR` / `TWI_THEME_SUCCESS` | No | Custom palette hex values, only applied when `TWI_THEME_NAME=custom`. |
| `TWI_STREAM_STATUS_MODE` | No | Enables (`auto`, default) or disables (`off`) polling Twitch Helix "Get Streams" for the status bar's real LIVE indicator. Requires `TWI_TWITCH_CLIENT_ID`/`TWI_TWITCH_OAUTH_TOKEN` either way. |
| `TWI_EMOTE_AUTOCOMPLETE_MODE` | No | Enables (`auto`, default) or disables (`off`) fetching real Twitch global/channel emotes for the Ctrl+E picker and quick-select row. Requires Twitch API credentials either way; `--mock` always uses a built-in sample list regardless of this setting. |
| `TWI_DEBUG_LOG` | No | Enables redacted structured debug logging when set to a true boolean value. Defaults to disabled. |
| `TWI_DEBUG_LOG_PATH` | No | Debug log file path. If omitted while logging is enabled, `twi` writes `debug.log` under the platform cache directory. Credential-shaped path values are redacted from config and diagnostic output. |

## Mode Values

Image modes:

- `auto`
- `off`
- `small`
- `normal`
- `large`

Avatar modes:

- `off`
- `initials`
- `image`

Emoji modes:

- `unicode`
- `image`

Emoji providers:

- `twemoji`
- `custom` with `emoji_url_template`

Emote modes:

- `text`
- `image`

Animation modes:

- `off`
- `reduced`
- `fast`

Theme names (`theme_name`):

- `claude` (default), `codex`, `btop`, `nord`, `dracula`, `gruvbox`,
  `solarized-dark`, `monokai`, `one-dark`, `tokyo-night`, `catppuccin-mocha`,
  `rose-pine`, `mono`
- `custom`, using the `theme_background`/`theme_foreground`/`theme_accent`/
  `theme_muted`/`theme_border`/`theme_surface`/`theme_warning`/`theme_error`/
  `theme_success` hex fields

Stream status mode (`stream_status_mode`) and emote autocomplete mode
(`emote_autocomplete_mode`):

- `auto` (default): enabled when Twitch API credentials are present
- `off`: disabled regardless of credentials

The current parser accepts mode values as strings. Animation mode currently supports `off`, `reduced`, and `fast`; `twi setup` rejects other animation modes, and `twi doctor` warns when a config file contains one. `avatar_mode = "image"` enables batched live avatar URL metadata lookup when Twitch API credentials are present. `emoji_provider = "twemoji"` uses the built-in pinned public Twemoji PNG template; `emoji_provider = "custom"` requires `emoji_url_template` with `{id}`. The Kitty renderer core exists behind the internal renderer boundary, and chat rows can substitute prepared image cells while preserving fallback text. Image, emoji, emote, and Kitty settings drive fallback rendering, diagnostics, renderer capability decisions, visible-row asset event scheduling, and live image-stack installation. Unknown `theme_name`, `stream_status_mode`, or `emote_autocomplete_mode` values fall back to their defaults and are reported by `twi doctor`'s feature-modes check.

## Example Config

This example matches the current flat parser. A richer TOML schema can be added later if needed.

```toml
twitch_username = "my_login"
twitch_oauth_token = ""
twitch_refresh_token = ""
twitch_client_id = ""
twitch_client_secret = ""
twitch_redirect_url = ""
default_channels = "somechannel"
enable_kitty_images = true
enable_mouse = true
image_mode = "auto"
avatar_mode = "initials"
emoji_mode = "image"
emoji_provider = "twemoji"
emoji_url_template = ""
emote_mode = "image"
animation_mode = "fast"
theme_name = "claude"
theme_background = ""
theme_foreground = ""
theme_accent = ""
theme_muted = ""
theme_border = ""
theme_surface = ""
theme_warning = ""
theme_error = ""
theme_success = ""
stream_status_mode = "auto"
emote_autocomplete_mode = "auto"
debug_logging = false
debug_log_path = ""
```

Do not paste a real token into shared docs, commits, logs, or support issues.
Shared config examples should leave secret values empty. Prefer `twi login` for
saved tokens on supported Unix platforms. If you keep credentials in this flat
config file too, keep the file private to your user account, for example with
`chmod 600`. `twi` does not automatically migrate values out of `config.toml`,
and flat config values still take precedence over saved credentials.

## CLI Commands And Flags

Implemented CLI commands include:

```sh
twi chat --channel <channel>
twi chat --mock
twi config show
twi config path
twi doctor
twi login
twi profile list
twi profile show
twi profile set <name>
twi setup
```

Debug logging flags are available on commands that can produce runtime
diagnostics:

```sh
twi chat --mock --channel demo --debug-log
twi chat --channel somechannel --debug-log --debug-log-path /tmp/twi-debug.log
twi login --debug-log
twi doctor --debug-log
```

`--debug-log=false` explicitly disables logging for that command even if
`TWI_DEBUG_LOG=true` or `debug_logging = true` is configured. Debug logs are
JSON lines written to a private file opened with create mode `0600`; parent
directories are created with `0700` when needed. Existing debug-log files that
are directories, symlinks, or allow group/other access are rejected. Unix builds
open existing debug-log files with a no-follow final path and validate the
opened file descriptor; non-Unix builds do not claim Unix no-follow or exact
ACL guarantees beyond the paths they can observe before and after open. Records
redact OAuth access tokens, refresh tokens, client secrets, bearer authorization
headers, callback codes/state, credential-shaped URL query values, and explicit
config secrets.
They also avoid raw `%+v`/`%#v` dumps of `ConnectionState`, `ChatMessage`,
raw IRC events, transport events, send results, raw tag maps, source URLs, and
transport errors. Review logs before sharing anyway because they can still
include non-secret identifiers such as channel names, message IDs, usernames,
event counts, and hostnames.

`twi login` is implemented as a browser/local-callback OAuth flow with a
`--dry-run` explanation path. Successful logins save credentials through the
restrictive fallback-file store on supported Unix builds; non-Unix builds keep
environment variables and private flat config files as the supported credential
path. `twi setup` is implemented for non-secret settings and login handoff.
Manual Kitty/Ghostty validation is still planned for a compatible graphics
terminal session.
Twitch IRC chat is current when username, OAuth token, and at least one channel
are configured. Live image startup is current for enabled asset kinds when
config, credentials, cache writability, and terminal capability allow it.
Multi-channel sidebar, command palette, selected-message inspect, and optional
mouse controls are current app behavior. Future flags for auth and mode
settings should follow the precedence rules above.

## Redacted Config Output

`twi config show` should print the effective non-secret configuration. For secrets, it should print only presence or a redacted placeholder:

```text
twitch_username = "my_login"
twitch_oauth_token = "[redacted]"
twitch_refresh_token = "[redacted]"
twitch_client_secret = "[redacted]"
```

It should not print token prefixes, token suffixes, or raw client secrets.

## Doctor Output

`twi doctor` prints one `[ok]` or `[warn]` line per diagnostic. Warnings do not
make the command fail; they identify missing credentials, missing config files,
unknown terminal capabilities, unavailable Kitty graphics signals, failed token
validation, or other degraded optional behavior.

The current diagnostics include:

- Config file path existence/readability.
- Saved credential-store presence, including the Unix credential-file path on
  supported builds.
- Twitch username, OAuth token, client ID, and client secret presence.
- Channel count, with a warning when no channel is configured.
- Token validation status, including Twitch identity, required and granted
  scopes, expiry, username mismatch, refresh availability, cancellation, and
  API-error states.
- Twitch IRC reachability to `irc.chat.twitch.tv:6697`.
- Terminal, true-color/256-color, configured mouse support, and mouse
  capability hints from environment variables.
- Kitty/Ghostty graphics signals and the active image fallback state.
- Live image-stack readiness: enabled, disabled, unsupported, degraded, or
  missing dependency.
- Cache directory writability using a single fixed-content probe file that is
  removed immediately, plus asset-cache pruning status for expired entries and
  the default size budget.
- Selected image, avatar, emoji, emote, animation, theme, stream-status, and
  emote-autocomplete modes, warning on unrecognized values.
- Stream-status polling readiness (`stream_status_mode` plus Twitch API
  credential presence) for the status bar's real LIVE indicator.

Secrets are never included in doctor details. OAuth tokens and client secrets
are redacted from validation and probe errors before output is formatted.

Twitch IRC exposes connect, reconnect, and disconnect callbacks for the
connection, not for each joined channel. `twi` copies those connection-level
states onto the configured channel states; channel-specific notices and chat
messages still route by their normalized channel names.
Manual reconnect (`ctrl+r` or the command palette) restarts the live IRC source
by closing the active transport before creating the replacement, while keeping
per-channel history, drafts, reply selection, scroll, and unread state in the
TUI model.

## Current vs Future Behavior

Current behavior:

- Load username and OAuth token from env/config and saved credentials on
  supported Unix platforms.
- Load channel names from `--channel`, `TWI_DEFAULT_CHANNELS`, or config.
- Load animation mode.
- Load basic image fallback settings.
- Load debug logging controls from config/env/CLI and write redacted JSON debug
  logs when explicitly enabled.
- Save successful `twi login` results through the supported credential store.
- Persist refreshed live IRC OAuth tokens through the supported credential
  store after auth refresh succeeds, with in-memory fallback and a redacted
  warning when saving is unavailable or fails.
- Create or update non-secret config with `twi setup`.
- Install live image asset services for allowed avatar, badge, emote, and emoji
  kinds when config, credentials, cache, and terminal checks pass.
- Redact secrets in all config output.
- Report effective diagnostics through `twi doctor` without requiring
  credentials.

Future target:

- Full terminal image mode controls.
- Cache sizing and pruning configuration.
