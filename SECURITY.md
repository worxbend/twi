# Security Policy

`twi` handles Twitch OAuth tokens, refresh tokens, client secrets, callback values, private config files, credential files, and debug logs. Treat all of those as sensitive.

## Supported Scope

Security fixes target the current `main` branch and the documented Unix-like terminal and Docker support boundary. Saved credentials are supported only on Go `unix` builds through the restrictive credential-file store. Windows and other non-Unix saved-credential backends are not supported.

## Reporting Issues

Do not open a public issue, pull request, screenshot, terminal recording, or discussion that contains real credentials, callback URLs with codes, debug logs with private context, or private config files.

If you discover a secret leak or credential-handling bug:

1. Rotate any exposed Twitch token, refresh token, or client secret immediately.
2. Prepare a minimal reproduction that uses fake credential-shaped values.
3. Share only redacted logs and the exact command path that produced the unsafe output.

If private reporting is not yet configured for the repository, create a public issue that describes the affected command and impact without including secrets. State that private details are available to the maintainer.

## Debug Log Safety

Debug logs are opt-in and redacted, but they can still include usernames, channel names, hostnames, non-secret IDs, terminal details, and timing. Review logs before sharing them. Never attach a debug log that was captured while real credentials or private channels were active unless you have manually checked it.

## Maintainer Checklist

- Reproduce with fake credential values.
- Add a regression test that scans for the fake secret markers.
- Keep fixes narrowly scoped to the leaking path.
- Update [docs/troubleshooting.md](docs/troubleshooting.md), [docs/auth.md](docs/auth.md), or [docs/config.md](docs/config.md) if user guidance changes.
