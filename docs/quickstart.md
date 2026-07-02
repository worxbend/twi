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
| `tab` | Move focus between chat and composer. |
| `?` | Expand or collapse help. |
| `pgup` / `pgdown` | Scroll chat history. |
| `up` / `down` | Select a message. |
| `r` | Reply to the selected message. |
| `esc` | Cancel reply mode. |
| `enter` | Send the composer text when connected live. |

## 4. Configure Live Twitch Chat

Live mode is partially shipped: it currently supports one Twitch channel over IRC with read, send, selected-message replies, and `/me` actions. Token identity/scope validation, multi-channel routing, and login/setup are still planned.

You need:

- Your Twitch login name.
- An IRC OAuth token.
- `chat:read` scope to read chat.
- `chat:edit` scope to send chat.

Username/token credentials currently come from environment variables or the flat config file. CLI flags currently override the channel and config path, not username or token values.

Environment variable setup:

```sh
export TWITCH_USERNAME="your_twitch_login"
export TWITCH_ACCESS_TOKEN="<your-twitch-access-token>"
export TWI_DEFAULT_CHANNELS="somechannel"
```

Then run:

```sh
go run ./cmd/twi chat --channel "$TWI_DEFAULT_CHANNELS"
```

Docker:

```sh
docker run --rm -it \
  -e TWITCH_USERNAME \
  -e TWITCH_ACCESS_TOKEN \
  twi:local chat --channel "$TWI_DEFAULT_CHANNELS"
```

The app also accepts the older canonical names `TWI_TWITCH_USERNAME` and `TWI_TWITCH_OAUTH_TOKEN`. If you use `TWITCH_ACCESS_TOKEN` without the `oauth:` prefix, `twi` adds the prefix before opening Twitch IRC.

If `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`, and `TWITCH_REFRESH_TOKEN` are set, `twi` tries one in-memory token refresh when Twitch IRC rejects the access token during login. It does not write the refreshed token back to `.env` yet.

## 5. Use A Config File Instead

Ask `twi` where it expects config:

```sh
go run ./cmd/twi config path
```

Create that file with flat `key = value` lines:

```toml
twitch_username = "your_twitch_login"
twitch_oauth_token = "PLACEHOLDER_TWITCH_OAUTH_TOKEN"
twitch_refresh_token = "PLACEHOLDER_TWITCH_REFRESH_TOKEN"
default_channels = "somechannel"
enable_kitty_images = true
image_mode = "auto"
avatar_mode = "initials"
emoji_mode = "unicode"
emote_mode = "text"
animation_mode = "fast"
```

The parser is intentionally small right now. Do not use nested TOML tables yet.

## 6. Diagnose Before Blaming The Terminal

Run:

```sh
go run ./cmd/twi doctor
```

Docker:

```sh
docker run --rm twi:local doctor
```

`doctor` reports config, credential presence, terminal hints, image fallback state, cache writability, and Twitch IRC reachability. It does not print raw OAuth tokens or client secrets.

Diagnostics are partially shipped: `doctor` reports credential presence and required-scope hints, but Helix-backed token identity, expiry, ownership, and scope validation are planned.

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
TWITCH_ACCESS_TOKEN=your_access_token_here
TWITCH_REFRESH_TOKEN=your_refresh_token_here
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

`missing Twitch credentials`: Set `TWITCH_USERNAME` and `TWITCH_ACCESS_TOKEN`, or run `twi chat --mock`.

`live Twitch chat currently supports one channel`: Pass one `--channel` value. Multi-channel live mode is planned.

Images look like text: Correct for now. Inline terminal image loading is planned; current rendering uses stable text, initials, Unicode, badge, and emote-token fallbacks.
