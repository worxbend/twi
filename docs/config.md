# Configuration

This document describes the configuration model for `twi`. The implemented parser is intentionally small and should be expanded deliberately as planned commands become real.

## Current State

- Config loading exists for flat `key = value` files, environment variables, and selected CLI overrides.
- `twi config show` and `twi config path` exist in the CLI.
- Mock chat is ready and does not require credentials or network access.
- One-channel live IRC read/send is partially shipped: `twi chat --channel <channel>` can read, send, reply, and send `/me` actions when username/token credentials are configured.
- Twitch credentials are currently read from environment variables or the flat config file. CLI flags currently override `--config` and `--channel`, not username or OAuth token values.
- Config output redacts OAuth tokens and client secrets.
- `twi doctor` diagnostics are partially shipped and report the effective config file path, credential presence,
  selected feature modes, Twitch IRC reachability, terminal hints, Kitty graphics
  signals, and cache directory writability without printing token or client
  secret values.
- `twi login`, multi-channel live chat, and inline terminal images are planned.
- Nested TOML tables are not implemented yet; keep config files flat.

## Precedence

Effective config should be resolved in this order, highest priority first:

1. CLI flags for `--config` and `--channel`.
2. Environment variables.
3. Config file.
4. Defaults.

This order lets users override local config for one command without editing files.
The interactive setup wizard is future work.

## Config Paths

Linux and macOS should follow XDG config rules:

```text
$XDG_CONFIG_HOME/twi/config.toml
~/.config/twi/config.toml
```

Windows should use the platform config directory.

The cache directory is the platform cache directory, such as:

```text
$XDG_CACHE_HOME/twi
~/.cache/twi
```

Cache contents should include non-secret metadata and downloaded assets only, such as avatar, emote, badge, and emoji data.

## Environment Variables

Supported variables:

| Variable | Secret | Purpose |
| --- | --- | --- |
| `TWI_TWITCH_USERNAME` | No | Twitch login for IRC auth. |
| `TWI_TWITCH_OAUTH_TOKEN` | Yes | Twitch IRC OAuth token with `chat:read` for live reads and `chat:edit` for composer sends. |
| `TWI_TWITCH_REFRESH_TOKEN` | Yes | Refresh token used for one in-memory OAuth refresh after live IRC auth failure. |
| `TWI_TWITCH_CLIENT_ID` | Usually no | Twitch app client ID for Helix/API calls. |
| `TWI_TWITCH_CLIENT_SECRET` | Yes | Client secret if a future OAuth flow needs it. |
| `TWITCH_USERNAME` | No | Dotenv alias for Twitch login. Canonical `TWI_TWITCH_USERNAME` wins if both are set. |
| `TWITCH_ACCESS_TOKEN` | Yes | Dotenv alias for OAuth token. A missing `oauth:` prefix is added for IRC use. Canonical `TWI_TWITCH_OAUTH_TOKEN` wins if both are set. |
| `TWITCH_REFRESH_TOKEN` | Yes | Dotenv alias for refresh token. Used for one in-memory OAuth refresh after live IRC auth failure. |
| `TWITCH_CLIENT_ID` | Usually no | Dotenv alias for client ID. |
| `TWITCH_CLIENT_SECRET` | Yes | Dotenv alias for client secret. |
| `TWI_DEFAULT_CHANNELS` | No | Default channel list. |
| `TWI_ENABLE_KITTY_IMAGES` | No | Enable or disable Kitty protocol image support. |
| `TWI_IMAGE_MODE` | No | Overall image mode. |
| `TWI_AVATAR_MODE` | No | Avatar rendering mode. |
| `TWI_EMOJI_MODE` | No | Standard emoji rendering mode. |
| `TWI_EMOTE_MODE` | No | Twitch emote rendering mode. |
| `TWI_ANIMATION_MODE` | No | Animation behavior. |

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

Emote modes:

- `text`
- `image`

Animation modes:

- `off`
- `reduced`
- `fast`
- `expressive`

The current parser accepts these values as strings. Animation mode currently supports `off`, `reduced`, and `fast`; unknown animation values are treated as `fast` by the shell. Image, avatar, emoji, emote, and Kitty settings currently drive fallback rendering and diagnostics only because inline image loading/rendering is not implemented.

## Example Config

This example matches the current flat parser. A richer TOML schema can be added later if needed.

```toml
twitch_username = "my_login"
twitch_oauth_token = "PLACEHOLDER_TWITCH_OAUTH_TOKEN"
twitch_refresh_token = "PLACEHOLDER_TWITCH_REFRESH_TOKEN"
twitch_client_id = ""
twitch_client_secret = ""
default_channels = "somechannel"
enable_kitty_images = true
image_mode = "auto"
avatar_mode = "initials"
emoji_mode = "unicode"
emote_mode = "text"
animation_mode = "fast"
```

Do not paste a real token into shared docs, commits, logs, or support issues.

## CLI Commands And Flags

Implemented CLI commands include:

```sh
twi chat --channel <channel>
twi chat --mock
twi config show
twi config path
twi doctor
```

`twi login`, multi-channel UI behavior, and inline terminal images are still planned. One-channel Twitch IRC chat is current when username, OAuth token, and channel are configured. Future flags for auth and mode settings should follow the precedence rules above.

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
- Twitch username, OAuth token, client ID, and client secret presence.
- Channel count, with a warning when no channel or multiple live IRC channels
  are configured.
- Token validation status, including Twitch identity, required and granted
  scopes, expiry, username mismatch, refresh availability, cancellation, and
  API-error states.
- Twitch IRC reachability to `irc.chat.twitch.tv:6697`.
- Terminal, true-color/256-color, and mouse capability hints from environment
  variables.
- Kitty/Ghostty graphics signals and the active image fallback state.
- Cache directory writability using a single fixed-content probe file that is
  removed immediately, plus asset-cache pruning status for expired entries and
  the default size budget.
- Selected image, avatar, emoji, emote, Kitty, and animation modes.

Secrets are never included in doctor details. OAuth tokens and client secrets
are redacted from validation and probe errors before output is formatted.

## Current vs Future Behavior

Current behavior:

- Load username and OAuth token from env/config.
- Load channel names from `--channel`, `TWI_DEFAULT_CHANNELS`, or config.
- Load animation mode.
- Load basic image fallback settings.
- Redact secrets in all config output.
- Report effective diagnostics through `twi doctor` without requiring
  credentials.

Future target:

- Interactive setup wizard.
- Secure token storage.
- Full terminal image mode controls.
- Cache sizing and pruning configuration.
- Persisting refreshed tokens safely after OAuth refresh succeeds.
