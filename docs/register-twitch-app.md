# Register Your Own Twitch Application

`twi` ships with **no built-in Twitch application**. It is designed to run
against *your own* Twitch app credentials, so your token requests, scopes, and
rate limits are yours and are never shared through a vendor-hosted client.

That means: before you can log in or use any Twitch-API-backed feature, you
register a personal application in the [Twitch developer
console](https://dev.twitch.tv/console/apps) and hand `twi` its **Client ID**
and **Client Secret**.

This guide covers registering that app and the **minimal configuration needed
to start**. For the credential model and precedence rules, read
[auth.md](auth.md) and [config.md](config.md); for symptoms and fixes, read
[troubleshooting.md](troubleshooting.md).

## Do You Even Need An App?

Pick the smallest path that covers what you want to do:

| Goal | Needs a registered app? | Minimum to start |
| --- | --- | --- |
| Try the UI, no Twitch account | No | nothing — `twi chat --mock --channel demo` |
| Read & send live chat with a token you already have | No | username + access token (`chat:read`, `chat:edit`) + a channel |
| Have `twi login` fetch a token for you | **Yes** | Client ID + Client Secret + registered redirect URL |
| Stream Info tab, stream markers, `/clip`, follower/sub counts, emote autocomplete | **Yes** | Client ID + access token (from `twi login`) |

The **intended, full-feature path is to register your own app** and let
`twi login` obtain a token with every scope. The token-only path exists for
users who already minted a Twitch OAuth token elsewhere and only want to
read/send chat.

## 1. Create The Application

1. Sign in at <https://dev.twitch.tv/console/apps> (enable 2FA on your Twitch
   account first if you have not — Twitch requires it to register apps).
2. Click **Register Your Application**.
3. Fill in:
   - **Name** — any unique name, e.g. `twi-<your-login>`.
   - **OAuth Redirect URLs** — add exactly:

     ```text
     http://localhost:1337/api/connect/twitch/callback
     ```

     This is `twi`'s built-in default. If you prefer a different localhost
     callback, add that instead and pass it later with `--redirect-uri`
     (see [Redirect URL rules](#redirect-url-rules)). Click **Add** so the URL
     appears in the list.
   - **Category** — anything reasonable (e.g. *Application Integration*).
   - **Client Type** — **Confidential**. `twi` uses the authorization-code flow
     with a client secret, which is a confidential client.
4. Click **Create**.

## 2. Copy The Client ID And Secret

Open the app you just created (**Manage**):

- **Client ID** is shown directly. It is not secret, but treat it as
  identifying.
- Click **New Secret** to generate a **Client Secret**. Twitch shows it once —
  copy it immediately. Regenerating a secret invalidates the previous one.

Keep the secret out of git, screenshots, and logs. `twi` redacts it from
`twi config show`, `twi doctor`, and debug logs, but you are responsible for how
you store it on disk.

## 3. Give The Credentials To `twi`

Set the Client ID and Secret through either environment variables or a private
flat config file. Environment variables take precedence.

Environment variables (canonical `TWI_`-prefixed names, and `.env`-style
aliases both work):

```sh
export TWI_TWITCH_CLIENT_ID="your_client_id"
export TWI_TWITCH_CLIENT_SECRET="your_client_secret"
# aliases: TWITCH_CLIENT_ID / TWITCH_CLIENT_SECRET
```

Or a private flat config file (find its path with `twi config path`). The
client ID is safe to store here; **leave the secret in the environment** rather
than a shared config, or keep the file private (`chmod 600`):

```toml
twitch_client_id = "your_client_id"
```

> `twi setup` can write non-secret values (username, client ID, channels, UI
> modes) for you and then hand off to login. It never writes tokens or the
> client secret.

## 4. Log In To Get A Token

With the Client ID and Secret set, run:

```sh
twi login
```

`twi` opens a browser to Twitch's authorization prompt, listens on the
localhost redirect URL, exchanges the callback for tokens, validates them, and
saves them **privately without printing them**. It requests these scopes:

| Scope | Enables |
| --- | --- |
| `chat:read` | Read Twitch IRC chat |
| `chat:edit` | Send Twitch IRC chat |
| `channel:manage:broadcast` | Stream Info tab (title/category/language/tags) and stream markers |
| `moderator:read:followers` | Follower count in the status line |
| `channel:read:subscriptions` | Subscriber count in the status line |
| `clips:edit` | `/clip` — create a clip of the active stream |
| `user:read:follows` | `/channels` picker autocomplete from your follow list |

On supported Unix platforms the tokens are saved to a restrictive private
credential file (`0700` directory, `0600` file, symlink rejection, no-follow
opens). On non-Unix builds saved credentials are disabled — use environment
variables or a private flat config file there.

Check the result without a browser first if you want:

```sh
twi login --dry-run     # explains scopes, redirect, and credential presence; contacts nothing
```

## 5. Start Chatting

```sh
twi chat --channel somechannel
# multiple channels:
twi chat --channel first --channel second
```

Verify everything resolved with:

```sh
twi doctor
```

`doctor` reports the effective config path, credential presence, OAuth
identity/expiry/scope validation, refresh availability, and IRC reachability —
without printing tokens or the client secret.

## Minimal Configurations, Summarized

**Mock — zero credentials:**

```sh
twi chat --mock --channel demo
```

**Live chat with a token you already have (no app needed):**

```sh
export TWITCH_USERNAME="your_twitch_login"
export TWITCH_ACCESS_TOKEN="<oauth token with chat:read + chat:edit>"
twi chat --channel somechannel
```

**Full experience with your own app (recommended):**

```sh
export TWI_TWITCH_CLIENT_ID="your_client_id"
export TWI_TWITCH_CLIENT_SECRET="your_client_secret"
twi login                       # registers the redirect URL above on the app first
twi chat --channel somechannel
```

`twi chat` hard-requires only an **access token** (the login is derived from
it). The **Client ID** additionally unlocks every
Helix-API-backed feature (Stream Info, markers, `/clip`, follower/subscriber
counts, category picker, emote autocomplete), and the **Client ID + Secret +
refresh token** let `twi` silently refresh an expired token on an IRC auth
failure instead of dropping you. Without the **secret**, the saved refresh
token cannot be redeemed and chat will disconnect when the access token
expires (about 4 hours); `twi chat` warns about this at startup.

## Redirect URL Rules

The callback URL registered on the Twitch app and the one `twi` listens on must
match **exactly** — scheme, host (`localhost` vs `127.0.0.1` are different),
port, path, and any trailing slash.

- Default: `http://localhost:1337/api/connect/twitch/callback`.
- Override per-run: `twi login --redirect-uri http://127.0.0.1:17643/oauth/twitch/callback`.
- Override persistently: set `twitch_redirect_url` in config, or
  `TWI_TWITCH_REDIRECT_URL` / `TWITCH_REDIRECT_URL` in the environment.
- Precedence: `--redirect-uri` flag > `twitch_redirect_url` config/env > built-in default.

Only `http` callbacks on `localhost`, `127.0.0.1`, or `::1` with an explicit
port and a non-root path are accepted — `twi` runs a local browser callback,
not a public one. Remember to click **Add** and then **Save** in the Twitch
console after entering the URL.

## Credential Environment Variables Reference

| Purpose | Canonical | `.env` alias |
| --- | --- | --- |
| Twitch login name | `TWI_TWITCH_USERNAME` | `TWITCH_USERNAME` |
| IRC access token | `TWI_TWITCH_OAUTH_TOKEN` | `TWITCH_ACCESS_TOKEN` |
| Refresh token | `TWI_TWITCH_REFRESH_TOKEN` | `TWITCH_REFRESH_TOKEN` |
| App client ID | `TWI_TWITCH_CLIENT_ID` | `TWITCH_CLIENT_ID` |
| App client secret | `TWI_TWITCH_CLIENT_SECRET` | `TWITCH_CLIENT_SECRET` |
| Redirect URL | `TWI_TWITCH_REDIRECT_URL` | `TWITCH_REDIRECT_URL` |
| Default channels | `TWI_DEFAULT_CHANNELS` | — |

When both a canonical `TWI_`-prefixed name and its alias are set, the canonical
name wins. `TWITCH_ACCESS_TOKEN` may be a plain token or an `oauth:`-prefixed
IRC token; `twi` adds the prefix before opening IRC. Never commit shell
profiles, `.env` files, config files, or logs that contain these values.
