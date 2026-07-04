# Authentication

This document describes the authentication model for `twi`. It covers the current environment/config-file credential path for Twitch IRC, the OAuth login command, and the restrictive credential-file fallback.

## Current State

- On supported Unix builds, `twi login` starts a Twitch authorization-code login through the browser/local-callback flow, validates the returned token, saves it through the private credential store, and reports identity/scope/storage status without printing token values. On non-Unix builds, the credential-file fallback is disabled before the browser opens, and users should use environment variables or a private flat config file.
- Mock chat is ready and needs no Twitch credentials.
- The MVP accepts Twitch credentials from environment variables, a local flat config file, or on supported Unix builds the private credential file. CLI flags currently override the config path and channels, not username or token values.
- Multi-channel live IRC read/send is partially shipped for configured credentials, including composer sends, selected-message replies, and `/me` actions.
- Multi-channel UX is partially shipped: the keyboard-first sidebar, command palette, optional mouse controls, and selected-message inspect panel are current behavior.
- `twi doctor` diagnostics are partially shipped; Twitch OAuth validation is wired into doctor and reports identity, expiry, required IRC scopes, username mismatch, and refresh availability without printing credential values.
- The internal credential storage boundary and Unix-only restrictive file
  fallback are wired into `twi login`, `twi chat`, `twi config show`, and
  `twi doctor`. `twi setup` can update non-secret config values and hand off
  to login.
  Richer EventSub/API chat support remains later work.

## MVP Credential Model

For Twitch IRC chat, the MVP needs:

- Twitch username.
- Twitch OAuth token.

Required IRC scopes:

- `chat:read` to receive chat.
- `chat:edit` to send chat.

The implemented config sources are, from highest to lowest priority:

1. CLI flags for `--config` and `--channel`.
2. Environment variables.
3. Config file.
4. Saved credential file values for empty credential fields on supported Unix builds.
5. Defaults.

`twi setup` is the current guided path for non-secret local configuration. It
can write username, Twitch app client ID, default channels, image modes, mouse
mode, emoji provider, and animation mode to `config.toml`, then either stop,
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

## Refresh On Auth Failure

When live Twitch IRC login fails with an authentication error, `twi` tries one OAuth refresh and reconnects with the refreshed access token if these values are configured:

- Twitch username.
- Current access token.
- Refresh token.
- Client ID.
- Client secret.

The refresh request is sent to Twitch's OAuth token endpoint. On supported Unix
builds, the refreshed access token and refresh token are saved through the
private credential store. If the credential store is unavailable or saving
fails, live chat keeps using the refreshed tokens in memory for the current
process and reports a redacted warning with next-step guidance. Refresh never
writes token material back to `.env` or the flat config file automatically.

If refresh fails, the user-facing error remains redacted and tells you to verify username, OAuth token, and `chat:read`.

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
http://127.0.0.1:17643/oauth/twitch/callback
```

Register that URI on the Twitch app, or pass `--redirect-uri` with another
localhost HTTP callback URL. Non-localhost callbacks and non-HTTP callbacks are
rejected because the CLI listener only supports a local browser callback.

For a credential-free or CI-safe command smoke, use:

```sh
twi login --dry-run
```

`--dry-run` explains the scopes, redirect URI, timeout, and client credential
presence without opening a browser, starting a callback listener, contacting
Twitch, printing secrets, or writing files.

The command requests the MVP scopes by default:

- `chat:read`
- `chat:edit`

On supported Unix builds, the command saves successful login results through
the restrictive credential file fallback. On non-Unix builds, the command stops
before opening the browser because the file fallback is disabled. Windows saved
credentials are planned through native Credential Manager rather than a
credential file.
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
the storage-owned marshal path deliberately reveals them for the private file.

The adapter uses the MVP scopes by default:

- `chat:read`
- `chat:edit`

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
and ordinary JSON encoding remain redacted. The fallback file implementation
must use `MarshalCredentialFile` and `ParseCredentialFile`; that marshal helper
is the explicit storage-owned reveal path for access and refresh tokens. Do not
use it for logs, diagnostics, screenshots, snapshots, or user-facing output.

No OS keychain backend is implemented today. The interface is shaped so one can
be added later after dependency, platform, and support tradeoffs are explicit.
[ADR 0007](adr/0007-use-windows-credential-manager-for-non-unix-credentials.md)
selects native Windows Credential Manager for the future Windows backend, but
users should not expect keychain behavior from the current binary.

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

Windows and other non-Unix builds do not have this file fallback enabled. The
current binary does not enforce Windows owner-only ACLs, DACL inheritance
rules, or reparse-point/no-follow protections for credential files, and it does
not pretend that Unix `0700`/`0600` mode semantics apply there. The selected
future Windows path is native Windows Credential Manager, not a JSON credential
file. Until that backend is implemented, `twi chat`, `twi config show`, and
`twi doctor` continue to work with environment variables and the flat config
file; `twi doctor` reports the disabled file fallback as a warning. `twi login`
reports a redacted actionable error before starting OAuth so tokens are not
obtained without a supported persistence path.

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

Startup currently checks that username and OAuth token are present after applying
env/config and saved credential values. `twi doctor` reports credential and
credential-file presence without printing raw credential values.

Current `twi doctor` behavior:

- Reports the credential file as loaded, missing, or failed.
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
`/oauth2/validate` endpoint. `twi doctor` uses that boundary now; startup
credential validation remains a later hardening step.

Missing or invalid auth should produce actionable errors without echoing the token value.

## MVP vs Future Scopes

MVP IRC scopes:

- `chat:read`
- `chat:edit`

Future EventSub or API chat work may require scopes such as:

- `user:read:chat`
- `user:write:chat`
- `user:bot`
- broadcaster-granted `channel:bot`

Do not request future scopes for the MVP unless the implementation actually needs them.

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
