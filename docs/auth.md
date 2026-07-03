# Authentication

This document describes the authentication model for `twi`. It covers the current environment/config-file credential path for Twitch IRC and the future interactive login flow.

## Current State

- `twi login` is planned, not implemented.
- Mock chat is ready and needs no Twitch credentials.
- The MVP accepts Twitch credentials from environment variables or a local flat config file. CLI flags currently override the config path and channels, not username or token values.
- Multi-channel live IRC read/send is partially shipped for configured credentials, including composer sends, selected-message replies, and `/me` actions.
- Multi-channel UX is partially shipped: the keyboard-first sidebar, command palette, optional mouse controls, and selected-message inspect panel are current behavior.
- `twi doctor` diagnostics are partially shipped; Twitch OAuth validation is wired into doctor and reports identity, expiry, required IRC scopes, username mismatch, and refresh availability without printing credential values.
- Later milestones may add interactive OAuth, setup wizard, secure credential storage, and richer EventSub/API chat support.

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
4. Defaults.

The interactive setup wizard is future work.

Suggested environment variables:

```sh
TWITCH_USERNAME="your_twitch_login"
TWITCH_ACCESS_TOKEN="<your-twitch-oauth-token>"
TWITCH_REFRESH_TOKEN="<your-twitch-refresh-token>"
TWITCH_CLIENT_ID="your_twitch_client_id"
TWITCH_CLIENT_SECRET="<your-twitch-client-secret>"
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

The refresh request is sent to Twitch's OAuth token endpoint. The refreshed access token and refresh token are kept in memory for the current process only; they are not written back to `.env` or the config file yet.

If refresh fails, the user-facing error remains redacted and tells you to verify username, OAuth token, and `chat:read`.

## OAuth Login Flow

`twi login` should eventually provide an interactive setup flow. The login
command is still not wired, but the internal HTTP adapter for Twitch's
authorization-code flow now exists behind the auth boundary.

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

This boundary does not wire the `twi login` command or persist tokens. Those
remain later tasks after the login redaction checkpoint. Any completed login
returns tokens only through typed results, leaving storage decisions to the
separate credential storage boundary.

The adapter uses the MVP scopes by default:

- `chat:read`
- `chat:edit`

It rejects missing, mismatched, expired, or duplicate OAuth state, denied
authorization callbacks, Twitch token/validation errors, missing required
scopes, client mismatches, context cancellation, and bounded request timeouts
with redacted actionable errors.

Future token storage should prefer:

1. OS keychain storage where practical.
2. Local config file fallback with restrictive file permissions.

Refresh tokens should be used when available and appropriate for the selected OAuth flow.

## Startup And Doctor Checks

Startup currently checks that username and OAuth token are present before opening
live IRC chat. `twi doctor` reports credential presence without printing raw
credential values.

Current `twi doctor` behavior:

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
