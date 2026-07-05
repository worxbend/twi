# Quickstart

This guide assumes nothing except a terminal and either Go or Docker. It keeps to what `twi` can do today.

## 1. Pick Your Path

Use Go when you are developing the project:

```sh
go run ./cmd/twi --help
```

Use Docker when you want a clean packaged run:

```sh
docker build -t twi:local .
docker run --rm twi:local --help
```

## 2. Run Mock Chat First

Mock mode is ready today and is the friendly sandbox. No Twitch account, no token, no network access.

```sh
go run ./cmd/twi chat --mock --channel demo
```

Docker:

```sh
docker run --rm -it twi:local chat --mock --channel demo
```

Compose:

```sh
docker compose run --rm mock
```

## 3. Learn The Keys

| Key | Action |
| --- | --- |
| `ctrl+p` | Open or close the command palette. |
| `tab` | Move focus between chat and composer. |
| `[` / `]` | Switch the active channel from chat focus. |
| `?` | Expand or collapse help. |
| `pgup` / `pgdown` | Scroll chat history. |
| `up` / `down` | Select a message. |
| `1` / `2` / `3` / `4` | Toggle local filters for mentions, broadcaster/mod/VIP messages, notices, and errors from chat focus. |
| `0` | Reset active-channel message filters. |
| `r` | Reply to the selected message. |
| `i` | Open or close the selected-message inspect panel. |
| `ctrl+l` | Clear the active channel's local chat history. |
| `ctrl+r` | Request a reconnect when the active chat source supports it. |
| `esc` | Close inspect mode or cancel reply mode. |
| `enter` | Send the composer text when connected live. |

Mouse support is enabled by default and can be disabled with
`enable_mouse = false` or `TWI_ENABLE_MOUSE=false`. Keyboard workflows remain
the primary path.

## 4. Configure Live Twitch Chat

Live mode is partially shipped: it supports one or more Twitch channels over IRC with startup token validation when Twitch OAuth validation is reachable, read, send, selected-message replies, `/me` actions, keyboard-first channel switching/sidebar state, command palette actions, optional mouse controls, and selected-message inspect diagnostics. `twi setup` can write non-secret config values and hand off to login. On supported Unix platforms, `twi login` can validate an OAuth browser/callback flow and save returned tokens through the restrictive credential-file fallback without printing them. Non-Unix builds keep saved credentials disabled.

You need:

- Your Twitch login name.
- An IRC OAuth token.
- `chat:read` scope to read chat.
- `chat:edit` scope to send chat.

Definitive malformed, expired, wrong-user, or missing-scope token states stop
live startup before IRC connects. If Twitch OAuth validation itself is
temporarily unreachable, `twi chat` warns and lets IRC authentication decide.

Username/token credentials currently come from environment variables, the flat config file, or saved credentials on supported Unix platforms. Environment and flat config values take precedence over saved credentials. CLI flags currently override channels and config path, not username or token values.

Guided setup:

```sh
go run ./cmd/twi setup
```

Automation-friendly setup:

```sh
go run ./cmd/twi setup --non-interactive --username your_twitch_login --channel somechannel --image-mode auto --emoji-provider twemoji --animation-mode fast
```

Setup does not ask for or write OAuth tokens, refresh tokens, callback codes,
OAuth state, authorization URLs, or client secrets. To hand off to login after
writing non-secret config, add `--login`; to exercise the bounded smoke path,
add `--login-dry-run`.

To check the login command without browser, network, or credentials:

```sh
go run ./cmd/twi login --dry-run
```

For the real OAuth flow, set `TWITCH_CLIENT_ID`/`TWITCH_CLIENT_SECRET` or the
canonical `TWI_TWITCH_CLIENT_ID`/`TWI_TWITCH_CLIENT_SECRET` names and register
`http://127.0.0.1:17643/oauth/twitch/callback` on the Twitch app. On supported
Unix platforms, the command validates returned tokens, saves them privately, and
never prints them. The credential file fallback uses a separate private
`credentials.json` under a `0700` platform config directory with `0600` file
permissions, symlink rejection, and no-follow file opens. Non-Unix builds keep
saved credentials disabled; use environment variables or a private flat config
file there.

Environment variable setup:

```sh
export TWITCH_USERNAME="your_twitch_login"
export TWITCH_ACCESS_TOKEN="<oauth token from Twitch>"
export TWI_DEFAULT_CHANNELS="somechannel"
```

Then run:

```sh
go run ./cmd/twi chat --channel "$TWI_DEFAULT_CHANNELS"
go run ./cmd/twi chat --channel onechannel --channel anotherchannel
```

Docker:

```sh
docker run --rm -it \
  -e TWITCH_USERNAME \
  -e TWITCH_ACCESS_TOKEN \
  twi:local chat --channel "$TWI_DEFAULT_CHANNELS"
```

The app also accepts the older canonical names `TWI_TWITCH_USERNAME` and `TWI_TWITCH_OAUTH_TOKEN`. If you use `TWITCH_ACCESS_TOKEN` without the `oauth:` prefix, `twi` adds the prefix before opening Twitch IRC.

If `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`, and `TWITCH_REFRESH_TOKEN` are set, `twi` tries one token refresh when Twitch IRC rejects the access token during login. On supported Unix platforms, refreshed tokens are saved through the private credential store. If saving is unavailable or fails, `twi` keeps the refreshed tokens in memory for the current chat session and reports a redacted warning. It does not write refreshed tokens back to `.env`.

## 5. Use A Config File Instead

Ask `twi` where it expects config:

```sh
go run ./cmd/twi config path
```

Create that file with flat `key = value` lines:

```toml
twitch_username = "your_twitch_login"
twitch_oauth_token = ""
twitch_refresh_token = ""
default_channels = "somechannel"
enable_kitty_images = true
image_mode = "auto"
avatar_mode = "initials"
emoji_mode = "unicode"
emote_mode = "text"
animation_mode = "fast"
```

The parser is intentionally small right now. Do not use nested TOML tables yet.
Prefer `twi setup` for non-secret config values and `twi login` for saved
tokens. Leave secret values empty in shared examples. If you keep any flat
config that contains real tokens, keep it private to your user account, for
example with `chmod 600`; flat config values still take precedence over saved
credentials.

## 6. Diagnose Before Blaming The Terminal

Run:

```sh
go run ./cmd/twi doctor
```

Docker:

```sh
docker run --rm twi:local doctor
```

`doctor` reports config, credential presence, Twitch OAuth identity/expiry/scope validation, refresh availability, username mismatch, terminal hints, image fallback state, cache writability, and Twitch IRC reachability. It does not print raw OAuth tokens or client secrets.

## 7. Use The Dotfile Shape

For Docker Compose, copy the tracked template:

```sh
cp .env.example .env
$EDITOR .env
docker compose run --rm live
```

The template uses this shape:

```dotenv
TWITCH_CLIENT_ID=your_client_id_here
TWITCH_CLIENT_SECRET=your_client_secret_here
TWITCH_ACCESS_TOKEN=paste_token_in_your_private_env_file
TWITCH_REFRESH_TOKEN=paste_refresh_value_in_your_private_env_file
TWITCH_USERNAME=your_twitch_login_here
TWITCH_CHANNEL=somechannel
```

`.env` is ignored by git. Keep the real file local.

## 8. Build A Local Binary

```sh
go build -o bin/twi ./cmd/twi
./bin/twi chat --mock --channel demo
```

## Common Fixes

`missing Twitch credentials`: Run `twi setup` for username/channels and `twi login` for saved OAuth credentials, set `TWITCH_USERNAME` and `TWITCH_ACCESS_TOKEN`, or run `twi chat --mock`.

Twitch IRC connection status is connection-level: Multi-channel live mode joins each configured channel, but Twitch IRC connect, reconnect, and disconnect callbacks are not independent per-channel events.

Images look like text: Expected when image mode is disabled, unsupported by the terminal, degraded, missing dependencies, still loading, or failed. Inline terminal image plumbing is partial and live resolver wiring is current, but manual Kitty/Ghostty validation is still planned; current rendering keeps stable text, initials, Unicode, badge, and emote-token fallbacks.
