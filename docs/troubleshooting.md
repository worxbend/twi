# Troubleshooting

This guide focuses on practical failure modes. Run `twi doctor` first; it is designed to report setup problems without printing secrets.

## Missing Credentials

If live chat reports missing credentials, choose one credential source:

```sh
export TWITCH_USERNAME="your_twitch_login"
export TWITCH_ACCESS_TOKEN="<oauth token from Twitch>"
go run ./cmd/twi chat --channel somechannel
```

On supported Unix platforms, prefer `twi login` for saved OAuth credentials:

```sh
export TWITCH_CLIENT_ID="your_client_id"
export TWITCH_CLIENT_SECRET="<client secret from Twitch>"
go run ./cmd/twi login
```

Flat config and environment values take precedence over saved credentials. If old env vars are still set, saved login results may not appear to take effect.

## Invalid Or Missing Scopes

Live IRC needs `chat:read` to read chat and `chat:edit` to send chat. `twi chat` validates token identity, expiry, username match, and scopes before IRC startup when Twitch OAuth validation is reachable. Definitive invalid states stop startup with redacted guidance. Transient validation failures warn and let IRC authentication decide.

## Login Does Not Open Or Save

`twi login` uses a local callback URL by default:

```text
http://localhost:1337/api/connect/twitch/callback
```

Register that redirect URI on the Twitch app. On non-Unix builds, saved credentials are unsupported and login stops before opening the browser. Use environment variables or a private flat config file if you manually build for an unsupported platform.

## Images Render As Text

Text fallbacks are expected when image mode is off, the terminal is not Kitty/Ghostty-compatible, cache paths are not writable, credentials for Twitch-backed assets are missing, downloads fail, image preparation fails, or manual Kitty/Ghostty validation has not been recorded for the environment.

Use:

```sh
go run ./cmd/twi doctor
```

Look for terminal image capability, cache writability, live image-stack readiness, and configured image/avatar/emoji/emote modes.

## Docker Cannot Reach Twitch Credentials

Pass credentials at runtime. Do not bake them into the image:

```sh
docker run --rm -it \
  -e TWITCH_USERNAME \
  -e TWITCH_ACCESS_TOKEN \
  twi:local chat --channel somechannel
```

If Docker itself fails before the container starts, check daemon access. Podman-equivalent checks are useful locally but do not replace exact Docker release evidence.

## Debug Logs

Enable debug logs only when you need diagnostics:

```sh
twi chat --channel somechannel --debug-log
twi doctor --debug-log
```

Logs redact known credential values, but they can still include usernames, channel names, hostnames, non-secret IDs, timing, and terminal details. Review debug logs before sharing them.

## Credential File Permission Errors

On Unix, the credential directory must be exactly `0700` and the credential file must be exactly `0600`. Existing symlinks, directories, group/world-readable files, and files with special mode bits are rejected. Fix permissions or move the unsafe file aside before running `twi login` again.

## Mock Mode Works But Live Mode Fails

Mock mode does not use Twitch credentials, network clients, or terminal image support. If mock mode works but live mode fails, check credentials, scopes, Twitch reachability, username mismatch, and local network access with `twi doctor`.
