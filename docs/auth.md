# Authentication

This document describes the authentication model for `twi`. It covers the current environment/config-file credential path for Twitch IRC and the future interactive login flow.

## Current State

- `twi login` is planned, not implemented.
- The MVP accepts Twitch credentials from environment variables or a local flat config file. CLI flags currently override the config path and channels, not username or token values.
- The first real Twitch transport uses Twitch IRC.
- Later milestones may add interactive OAuth and richer EventSub/API chat support.

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
TWI_TWITCH_USERNAME=<your Twitch login>
TWI_TWITCH_OAUTH_TOKEN=<your OAuth token>
TWI_TWITCH_CLIENT_ID=<client id when Helix/API features need it>
TWI_TWITCH_CLIENT_SECRET=<client secret only if a future flow needs it>
```

Do not commit shell profiles, `.env` files, config files, screenshots, or logs that contain these values.

## Planned Login Flow

`twi login` should eventually provide an interactive setup flow. The preferred path is an OAuth device-code flow if Twitch supports the required scopes for this application. If not, the fallback is a local callback flow.

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
- Reports token validation as `not available` when an OAuth token is present but
  no token validation client is wired in yet.
- Names the required IRC scopes, `chat:read` and `chat:edit`, when scope
  validation is unavailable or fails.
- Redacts OAuth tokens and client secrets from diagnostic details and validation
  errors.

When richer auth validation is implemented, startup and `twi doctor` should also
check:

- A token is present when real Twitch chat is requested.
- The token is valid.
- The token belongs to the configured username.
- The token has required scopes for the enabled transport.
- The token has not expired.

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
