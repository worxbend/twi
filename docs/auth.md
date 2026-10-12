# Authentication

This document describes the authentication model for `twi`. It covers the environment/config credential path, authorization-code and device OAuth login, explicit named accounts, and restrictive Unix credential storage. To register your own Twitch application and get its client ID/secret (required for `twi login` and Twitch-API features), read [register-twitch-app.md](register-twitch-app.md). For setup symptoms and fixes, read [troubleshooting.md](troubleshooting.md); for the full docs map, read [index.md](index.md).

## Current State

- On supported Unix builds, `twi login` starts a Twitch authorization-code login through the browser/local-callback flow, validates the returned token, saves it through the private credential store, and reports identity/scope/storage status without printing token values. Non-Unix builds stop before the browser opens, and users should use environment variables or a private flat config file.
- Mock chat is ready and needs no Twitch credentials.
- Credentials come from environment variables, a flat config file, or supported Unix credential storage. Explicit `--account NAME` selects that private account and never falls back to another environment/default token.
- Multi-channel IRC and EventSub reading, recorder/history commands, acknowledged Helix sending, selected-message replies, and native IRC actions are implemented. See [automation](automation.md) for transport-specific receipt semantics.
- Multi-channel UX includes: the keyboard-first sidebar, command palette, optional mouse controls, and selected-message inspect panel are current behavior.
- Twitch OAuth validation is wired into both `twi doctor` and live chat startup. It reports or enforces identity, expiry, required IRC scopes, and refresh availability without printing credential values, and supplies the IRC login that live chat authenticates with.
- The internal credential storage boundary and Unix-only restrictive file
  fallback are wired into `twi login`, `twi chat`, `twi config show`, and
  `twi doctor`. `twi setup` can update
  non-secret config values and hand off to login.
  EventSub/API chat scopes are selected through explicit login capabilities.

## Credential sources

For Twitch IRC chat, the client needs:

- Twitch username.
- Twitch OAuth token.

Required IRC scopes:

- `chat:read` to receive chat.
- `chat:edit` to send chat.

The implemented config sources are, from highest to lowest priority:

1. CLI flags for `--config` and `--channel`.
2. Environment variables.
3. Config file.
4. Saved credential values for empty credential fields on supported Unix platforms.
5. Defaults.

`twi setup` is the current guided path for non-secret local configuration. It
can write username, Twitch app client ID, default channels, avatar mode, mouse
mode, and animation mode to `config.toml`, then either stop,
run `twi login`, or run `twi login --dry-run`.

Suggested environment variables:

```sh
TWITCH_USERNAME="your_twitch_login"
TWITCH_ACCESS_TOKEN="<oauth token from Twitch>"
TWITCH_REFRESH_TOKEN="<refresh token from Twitch>"
TWITCH_CLIENT_ID="your_twitch_client_id"
TWITCH_CLIENT_SECRET="<client secret from Twitch>"
```

The canonical `TWI_TWITCH_USERNAME`, `TWI_TWITCH_OAUTH_TOKEN`, `TWI_TWITCH_REFRESH_TOKEN`, `TWI_TWITCH_CLIENT_ID`, and `TWI_TWITCH_CLIENT_SECRET` names still work and take priority when both forms are set. `TWITCH_ACCESS_TOKEN` may be either a plain token or an `oauth:`-prefixed IRC token.

Do not commit shell profiles, `.env` files, config files, screenshots, or logs that contain these values.

## Refresh on authentication failure

The shared session authorization manager validates identity and the selected operation’s scopes. On an explicit expired/malformed token or authentication rejection, it coordinates refresh through a private account lock when these values are configured:

- Twitch username.
- Current access token.
- Refresh token.
- Client ID.
- Client secret.

> **The client secret is the one `twi login` does not save.** The credential
> store deliberately holds no secrets beyond the tokens themselves, so a
> login-only setup has a refresh token it cannot redeem. Twitch access tokens
> last roughly four hours, which means live chat will disconnect partway
> through a long session and will not recover on its own.
>
> To get unattended refresh, set `TWI_TWITCH_CLIENT_SECRET` (or
> `twitch_client_secret` in the flat config) to the secret from your Twitch
> application. Otherwise, re-run `twi login` when chat drops.
>
> `twi chat` prints a warning at startup when a refresh token is present
> without a secret, and `twi doctor`'s **client secret** check reports the
> same thing.

Refresh uses Twitch’s OAuth endpoint. Saved-account refresh reloads credentials
under the shared account lock and persists the rotated pair atomically, so a
second process can reuse that rotation. Persistence failure is reported;
headless commands fail rather than claim durable rotation. The IRC compatibility
path may retain active tokens in memory while reporting its persistence warning.
Explicit environment credentials stay in memory and do not overwrite saved
accounts. Refresh never writes tokens back to `.env` or flat config automatically.

If refresh fails, the user-facing error remains redacted and tells you to verify username, OAuth token, and `chat:read`.

Before opening the IRC session, `twi chat` also validates the configured access
token through Twitch OAuth when validation is reachable. Malformed, expired,
wrong-user, or missing-scope tokens stop live startup before IRC connects.
Transient validation failures print a redacted warning and continue to IRC
authentication so a temporary validation outage does not block chat when IRC
would still accept the token.

## OAuth Login Flow

`twi login` provides the current interactive OAuth entry point. It uses Twitch's
authorization-code flow, opens the authorization prompt in a browser, listens
for a localhost HTTP callback, exchanges the callback code for tokens, validates
the returned access token, and prints only safe identity and scope status.

Default command behavior:

```sh
export TWI_TWITCH_CLIENT_ID="your_twitch_client_id"
export TWI_TWITCH_CLIENT_SECRET="<client secret from Twitch>"
twi login
```

The default redirect URI is:

```text
http://localhost:1337/api/connect/twitch/callback
```

Register that URI on the Twitch app, or pass `--redirect-uri` with another
localhost HTTP callback URL. Non-localhost callbacks and non-HTTP callbacks are
rejected because the CLI listener only supports a local browser callback.

If your Twitch app already has a different localhost callback, use that exact
value with `--redirect-uri`. For example:

```sh
twi login --redirect-uri http://127.0.0.1:17643/oauth/twitch/callback
```

In the Twitch developer console, type or paste the URL, click **Add**, then
click **Save** before running `twi login`. The value must match exactly,
including `localhost` versus `127.0.0.1`, port, path, scheme, and trailing slash.

To avoid passing `--redirect-uri` on every run, set it once in config instead:

```toml
twitch_redirect_url = "http://127.0.0.1:17643/oauth/twitch/callback"
```

or `TWI_TWITCH_REDIRECT_URL` (`TWITCH_REDIRECT_URL` also works as a dotenv
alias; the canonical `TWI_`-prefixed name wins if both are set). Precedence is:
an explicit `--redirect-uri` flag wins, then `twitch_redirect_url`/
`TWI_TWITCH_REDIRECT_URL`/`TWITCH_REDIRECT_URL`, then the built-in
default shown above. The redirect URL is not a secret and is not redacted in
`twi config show`, though credential-shaped query values in it are still
flagged like any other URL-shaped config value.

For a credential-free or CI-safe command smoke, use:

```sh
twi login --dry-run
```

`--dry-run` explains the scopes, redirect URI, timeout, and client credential
presence without opening a browser, starting a callback listener, contacting
Twitch, printing secrets, or writing files.

To bootstrap a starter config file before your first login, add
`--write-default-config`:

```sh
twi login --write-default-config
```

This writes the effective non-secret config (built-in defaults merged with
any credentials already present in the environment) to the effective config
path — but only if no file exists there yet; an existing `config.toml` is
never overwritten. Combine it with `--dry-run` to inspect the path without
writing anything (`--write-default-config` is a no-op during `--dry-run`,
consistent with dry-run never writing files).

The command requests these scopes by default:

- `chat:read`
- `chat:edit`
- `channel:manage:broadcast` (Stream Info tab: view/edit title, category, language, tags; Misc tab: create/list stream markers)
- `moderator:read:followers` (status line: follower count)
- `channel:read:subscriptions` (status line: subscriber count)
- `clips:edit` (`/clip` chat command: create a clip of the active stream)
- `user:read:follows` (`/channels` picker: autocomplete from the channels you follow)

On supported Unix builds, the command saves successful login results through
the restrictive credential file fallback. On non-Unix builds, the command stops
before opening the browser because no saved-credential backend is enabled.
Environment variables and flat config values still take precedence when
present, so remove duplicates from shell profiles, `.env`, or `config.toml`
after confirming saved credentials work.

`twi setup --login` delegates to this login command after writing only
non-secret config values. `twi setup --login-dry-run` delegates to the bounded
login smoke path for CI and local checks.

The internal login boundary now lives in `internal/auth`. It defines:

- `LoginFlow`, with separate `BeginLogin` and `CompleteLogin` methods so CLI,
  storage, Twitch HTTP adapters, and future UI code do not depend on each
  other.
- `LoginRequest`, `LoginChallenge`, `LoginCallback`, `LoginResult`, and
  `TokenSet` request/response types.
- `ScopeChatRead`, `ScopeChatEdit`, and helpers for the default chat read/send
  scope set.
- `Secret` and `Redactor` contracts. Token values, refresh tokens, OAuth state,
  callback codes, authorization URLs, and client secrets are redacted by default
  when formatted; adapters must deliberately reveal them only for OAuth HTTP
  requests or test assertions.
- `FakeLoginFlow` for unit tests.
- `TwitchOAuthLoginFlow`, a context-aware authorization-code adapter that builds
  Twitch authorization URLs, validates callback state, exchanges callback codes
  for access and refresh tokens, validates the returned token through Twitch's
  OAuth validation endpoint, checks that the validated token belongs to the
  configured client ID, and returns token material only through typed `Secret`
  fields.

The CLI converts completed login results into `storage.CredentialRecord` and
saves them through `CredentialStore`. Token values remain typed secrets until
the storage-owned marshal path deliberately reveals them for the private Unix
credential file.

The authorization-code adapter falls back to IRC chat scopes when a caller supplies no
explicit scope list:

- `chat:read`
- `chat:edit`

`twi login` supplies the list selected by `--capability` (default `tui`), so
this fallback only applies to other callers omitting `LoginRequest.Scopes`.
Device login validates the granted scopes and returned client/user identity
before private persistence.

It rejects missing, mismatched, expired, or duplicate OAuth state, denied
authorization callbacks, unsupported callback listeners, unsupported browser
opening, Twitch token/validation errors, missing required scopes, client
mismatches, context cancellation, and bounded request timeouts with redacted
actionable errors.

## Credential Storage Boundary

`internal/storage` defines and implements the credential persistence boundary
for login/setup work:

- `CredentialStore` with load, save, and delete methods.
- `CredentialRecord`, the storage-owned DTO for Twitch identity, client ID,
  access token, refresh token, token type, scopes, expiry, and update time.
- `MemoryCredentialStore` and `FailingCredentialStore` fakes for unit tests.
- `CredentialFileStore`, the restrictive Unix-only local fallback implementation.
- A versioned fallback JSON record format for Twitch credentials.

`CredentialRecord` stores token values as `auth.Secret`, so ordinary formatting
and ordinary JSON encoding remain redacted. The Unix file implementation must
use `MarshalCredentialFile` and `ParseCredentialFile`; that marshal helper is
the explicit storage-owned reveal path for access and refresh tokens. Do not
use it for logs, diagnostics, screenshots, snapshots, or user-facing output.

The credential-file fallback is supported only on Go `unix` builds today,
including Linux and macOS. It is a separate local credential file under the
platform config directory, for example:

```text
$XDG_CONFIG_HOME/twi/credentials.json
~/.config/twi/credentials.json
```

It is not the existing flat `config.toml` file. On supported Unix builds, the
fallback implementation:

- creates credential directories with `0700` permissions;
- creates credential files with `0600` permissions;
- writes through a temporary file and same-directory rename where practical;
- rejects symlinks at the credential directory or file path;
- opens existing credential files with a no-follow file open;
- rejects existing credential files or directories whose permissions do
  not match those exact modes;
- uses redacted errors when file data, token values, provider errors, or migration
  failures are reported.

Non-Unix builds do not have this file fallback enabled and do not have a native
saved-credential backend in this project. On those platforms, `twi chat`,
`twi config show`, and `twi doctor` continue to work with environment variables
and the flat config file. `twi login` reports a redacted actionable error
before starting OAuth so tokens are not obtained without a supported
persistence path.

The fallback JSON format is versioned. Version 1 records only Twitch OAuth
credential material and safe identity metadata:

```json
{
  "version": 1,
  "twitch": {
    "user_id": "12345",
    "login": "your_twitch_login",
    "display_name": "YourDisplayName",
    "client_id": "your_twitch_client_id",
    "access_token": "<stored access token>",
    "refresh_token": "<stored refresh token>",
    "token_type": "bearer",
    "scopes": ["chat:read", "chat:edit"],
    "expires_at": "2026-07-03T13:00:00Z",
    "updated_at": "2026-07-03T12:00:00Z"
  }
}
```

Migration is explicit only. `twi` does not silently copy secrets from
environment variables or the flat config file into credential storage during
setup or config loading. `twi login` saves credentials after a successful
user-authorized OAuth login; live IRC auth refresh saves only newly refreshed
tokens after Twitch has accepted the configured refresh flow. `twi setup
--login` is an explicit handoff to the login/storage boundary. Remove
duplicate secrets from shell profiles, `.env`, or `config.toml` if you no
longer want those sources to take precedence.

Refresh tokens should be used when available and appropriate for the selected OAuth flow.

## Startup And Doctor Checks

Startup requires an OAuth token after applying env/config and saved credential
values. A username is not required: the IRC login is derived from whichever
account the validated token belongs to, because Twitch requires the IRC login
to be that account. A configured `twitch_username` is only a fallback for when
token validation cannot reach Twitch, and one naming a different account is
reported as stale and ignored. `twi doctor` reports
credential and credential-store presence without printing raw credential values.
On Unix this check names the credential file path. On non-Unix platforms it
warns that saved credential persistence is disabled and points to env/config
credentials.

Current `twi doctor` behavior:

- Reports the selected credential store as loaded, missing, or failed.
- Reports Twitch username, OAuth token, client ID, and client secret as
  `present` or `missing`.
- Reports missing username or OAuth token as warnings because mock mode and
  non-network diagnostics can still run.
- Reports token validation as `not available` only when no validator is supplied
  by the caller; the CLI `doctor` command wires the live OAuth validator.
- Names the required IRC scopes, `chat:read` and `chat:edit`, and reports
  granted or missing scopes when validation completes.
- Uses the internal token validation boundary to distinguish malformed,
  expired, wrong-user, missing-scope, canceled, API-error, and valid credentials
  without stopping the rest of the report.
- Redacts OAuth tokens and client secrets from diagnostic details and validation
  errors.

The current validation boundary can represent:

- The validated Twitch identity.
- Token expiry.
- Granted and missing scopes.
- Username ownership mismatch.
- Refresh availability.

The live Twitch HTTP adapter validates access tokens through Twitch OAuth's
`/oauth2/validate` endpoint. `twi doctor` and live chat startup use that
boundary now.

Missing or invalid auth should produce actionable errors without echoing the token value.

## Implemented capability scopes

IRC scopes:

- `chat:read`
- `chat:edit`

Stream Info and Misc tab scope (view/edit the broadcaster's own title,
category, language, and tags; create/list stream markers; unrelated to IRC
readiness, so `twi doctor` and live chat startup do not gate on it):

- `channel:manage:broadcast`

Status line scopes (follower/subscriber counts; also unrelated to IRC
readiness):

- `moderator:read:followers`
- `channel:read:subscriptions`

`/clip` chat command scope (create a clip of the active stream; also
unrelated to IRC readiness):

- `clips:edit`

`/channels` picker scope (autocomplete from your follow list; also unrelated
to IRC readiness — without it the picker still accepts any typed name):

- `user:read:follows`

EventSub/API capabilities use:

- `user:read:chat`
- `user:write:chat`
- `user:bot`
- broadcaster-granted `channel:bot`

Request scopes for the selected operation. `eventsub-read` uses
`user:read:chat`; `chat-send` uses `user:write:chat`; `bot-send` also requests
`user:bot`, and `channel-bot` requests the broadcaster’s `channel:bot` grant.
`moderation-read` and `validation` cover documented moderation-event scopes
and verified moderated-channel discovery. `validation` additionally includes
IRC sending/reading and message deletion for controlled acceptance tests.
Scopes authorize API operations; they do not prove moderator/VIP roles.
See [live authorization](live-authorization.md) and [send transports](send-transports.md).

## Secret Redaction Rules

Secrets must be redacted from:

- `twi config show`
- `twi doctor`
- debug logs
- error messages
- test snapshots
- raw message inspection panels
- issue templates or support bundles

Use labels such as `<redacted>` or show only whether a secret is present. Do not print token prefixes or suffixes unless the project intentionally documents that policy later.

Debug logging is opt-in with `debug_logging = true`, `TWI_DEBUG_LOG=true`, or
the `--debug-log` flag on `chat`, `login`, and `doctor`. Auth debug records log
phase names, scope counts, identity names, refresh availability, status, and
sanitized errors. They do not log authorization URLs, callback codes, OAuth
state values, access tokens, refresh tokens, client secrets, auth headers, raw
config secrets, raw token validation responses, or raw transport errors.

## Troubleshooting Targets

Planned user-facing errors should distinguish:

- Missing username.
- Missing token.
- Invalid token.
- Token for a different account.
- Missing `chat:read`.
- Missing `chat:edit`.
- Expired token.
- Phone verification or account restrictions.
- Channel ban or timeout.
- Twitch reachability or network failure.

The app should keep these messages specific enough to fix the problem while never exposing credentials.

## Automation and durable history

Producer, consumer, foreground recorder, and local history commands are implemented
through UI-independent services and explicit public DTOs. See [Automation](automation.md) for
capabilities, examples, coverage/cursor guarantees, retention, and validation limits.
Live Twitch observation parity requires controlled credentialed validation.

## Device login without a local browser

```sh
twi login --device --account validation --capability validation --timeout 30m
```

The command displays Twitch’s activation URL and user code. Complete consent
in a browser signed in to the intended Twitch account. No localhost callback or
local browser is needed. The private device code, access token, refresh token,
and client secret are excluded from public output and logs. The command polls
at the provider’s interval, slows down when instructed, respects cancellation
and expiry, validates identity/scopes/client, and saves the grant privately.
`--device` conflicts with an explicit `--redirect-uri`; `--dry-run` performs
no network or credential-store access.

The client ID is required. Confidential applications use their configured
client secret, which is not stored with the tokens. The named account can be
selected in subsequent commands with `--account validation`.
See Twitch’s [device grant specification](https://dev.twitch.tv/docs/authentication/getting-tokens-oauth/#device-code-grant-flow).
