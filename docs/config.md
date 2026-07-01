# Configuration

This document describes the configuration model for `twi`. The repo is still in bootstrap, so the implemented parser is intentionally small and should be expanded deliberately as new commands become real.

## Current State

- Config loading exists for flat `key = value` files, environment variables, and selected CLI overrides.
- `twi config show` and `twi config path` exist in the bootstrap CLI.
- `twi chat --channel <channel>` uses `TWI_TWITCH_USERNAME` and `TWI_TWITCH_OAUTH_TOKEN` for the current one-channel Twitch IRC read path.
- Config output redacts OAuth tokens and client secrets.
- Nested TOML tables are not implemented yet; keep bootstrap config files flat.

## Precedence

Effective config should be resolved in this order, highest priority first:

1. CLI flags.
2. Environment variables.
3. Config file.
4. Interactive setup wizard or defaults.

This order lets users override local config for one command without editing files.

## Config Paths

Linux and macOS should follow XDG config rules:

```text
$XDG_CONFIG_HOME/twi/config.toml
~/.config/twi/config.toml
```

Windows should use the platform config directory.

The planned cache directory is the platform cache directory, such as:

```text
$XDG_CACHE_HOME/twi
~/.cache/twi
```

Cache contents should include non-secret metadata and downloaded assets only, such as avatar, emote, badge, and emoji data.

## Environment Variables

Planned variables from `PLAN.md`:

| Variable | Secret | Purpose |
| --- | --- | --- |
| `TWI_TWITCH_USERNAME` | No | Twitch login for IRC auth. |
| `TWI_TWITCH_OAUTH_TOKEN` | Yes | Twitch IRC OAuth token with `chat:read` for live reads and `chat:edit` for composer sends. |
| `TWI_TWITCH_CLIENT_ID` | Usually no | Twitch app client ID for Helix/API calls. |
| `TWI_TWITCH_CLIENT_SECRET` | Yes | Client secret if a future OAuth flow needs it. |
| `TWI_DEFAULT_CHANNELS` | No | Default channel list. |
| `TWI_ENABLE_KITTY_IMAGES` | No | Enable or disable Kitty protocol image support. |
| `TWI_IMAGE_MODE` | No | Overall image mode. |
| `TWI_AVATAR_MODE` | No | Avatar rendering mode. |
| `TWI_EMOJI_MODE` | No | Standard emoji rendering mode. |
| `TWI_EMOTE_MODE` | No | Twitch emote rendering mode. |
| `TWI_ANIMATION_MODE` | No | Animation behavior. |

## Planned Modes

Image modes:

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

The MVP may support only a subset. Unsupported values should fail clearly or be ignored only when documented.

## Example Config

This example matches the current flat bootstrap parser. A richer TOML schema can be added later if needed.

```toml
twitch_username = "my_login"
twitch_oauth_token = "REDACTED"
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

Bootstrap CLI commands include:

```sh
twi chat --channel <channel>
twi chat --mock
twi config show
twi config path
twi doctor
```

`twi login` and multi-channel UI behavior are still planned. One-channel Twitch IRC chat is current when username, OAuth token, and channel are configured. Additional flags for auth, image modes, animation modes, and config paths should follow the precedence rules above.

## Redacted Config Output

`twi config show` should print the effective non-secret configuration. For secrets, it should print only presence or a redacted placeholder:

```text
twitch_username = "my_login"
twitch_oauth_token = "[redacted]"
twitch_client_secret = "[redacted]"
```

It should not print token prefixes, token suffixes, or raw client secrets.

## MVP vs Future Behavior

MVP target:

- Load username and OAuth token from env/config/flags.
- Load one or more channel names.
- Load animation mode.
- Load basic image fallback settings.
- Redact secrets in all config output.

Future target:

- Interactive setup wizard.
- Secure token storage.
- Full terminal image mode controls.
- Cache sizing and pruning configuration.
- Doctor output that explains effective config and degraded states.
