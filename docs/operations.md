# Foreground recorder supervision

`twi record` stays in the foreground. The examples in `examples/operations`
provide explicit supervision with private state, graceful signals, bounded
restarts, and durable local history. Creating these files does not install or
start a service. Choose only one writer for each history database.

First install the matching release binary and explicitly create a named account:

```sh
twi login --account recorder --capability chat-read
```

Use `eventsub-read` instead when selecting `--transport eventsub`. Read-only
recording needs no send scope. An application secret needed for automatic token
refresh belongs in private local configuration, not command-line arguments or
checked-in service files. The recorder never opens a browser or implicitly logs
in when started by a supervisor.

## Linux user service

The supplied unit runs as your existing user. It does not require a system-wide
service or root account. Configure the environment before enabling it:

```sh
umask 077
mkdir -p "$HOME/.config/twi" "$HOME/.local/state/twi" "$HOME/.cache/twi"
install -m 0600 examples/operations/recorder.env.example "$HOME/.config/twi/recorder.env"
# Edit that private file: set your explicit channel and chosen transport.
mkdir -p "$HOME/.config/systemd/user"
install -m 0600 examples/operations/twi-record.service "$HOME/.config/systemd/user/twi-record.service"
systemctl --user daemon-reload
systemctl --user enable --now twi-record.service
journalctl --user -u twi-record.service -f
```

The unit uses a 0077 umask, 20-second SIGTERM grace period, and success statuses
130/143 for deliberate signal shutdown. Network failures restart after 15 seconds,
with a five-start limit per 300 seconds. Usage, authorization, and storage errors
(2/3/7) stop automatic restart so an unresolved fault cannot loop indefinitely.
After resolving such a fault, run `systemctl --user reset-failed` and restart the
unit explicitly. `systemctl --user stop twi-record.service` requests graceful
flush; disable it with `systemctl --user disable twi-record.service`.

User services normally stop when your session ends. Enabling user lingering is a
separate administrative choice; this project does not enable it automatically.

## macOS LaunchAgent

The plist runs the foreground command as your current user. Replace `YOUR_USER`
and `YOUR_CHANNEL` with absolute paths and an explicit channel before copying.
Launchd does not expand shell variables in `ProgramArguments`. Use the same named
account and transport chosen during login.

```sh
umask 077
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs/twi" "$HOME/.local/state/twi"
# Edit a local copy of examples/operations/io.github.worxbend.twi-record.plist.
install -m 0600 /path/to/edited.plist "$HOME/Library/LaunchAgents/io.github.worxbend.twi-record.plist"
plutil -lint "$HOME/Library/LaunchAgents/io.github.worxbend.twi-record.plist"
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/io.github.worxbend.twi-record.plist"
```

The umask is decimal 63 (octal 077), restarts are throttled to 30 seconds, and the
exit grace period is 20 seconds. Output paths point to a private log directory.
Boot out the agent before repairing authorization/configuration failures or
rotating its log files:

```sh
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/io.github.worxbend.twi-record.plist"
```

The plist parser has been validated on Linux with Python's standard `plistlib`.
Actual launchd execution requires macOS; no LaunchAgent was installed here.

## Explicit container supervision

Build the nonroot image, then initialize the named-account credentials in its
private config volume. This is a separate interactive login step:

```sh
docker build -t twi:local .
TWI_RECORD_CHANNEL=yourchannel docker compose -f examples/operations/compose.record.yaml run --rm recorder login --account recorder --capability chat-read --device
TWI_RECORD_CHANNEL=yourchannel docker compose -f examples/operations/compose.record.yaml up -d recorder
TWI_RECORD_CHANNEL=yourchannel docker compose -f examples/operations/compose.record.yaml logs -f recorder
TWI_RECORD_CHANNEL=yourchannel docker compose -f examples/operations/compose.record.yaml stop -t 20 recorder
```

The service keeps UID/GID 10001, a read-only root filesystem, writable private
config/cache/state volumes, an init process, SIGTERM, and five restart attempts.
It includes no environment-file loader; credentials stay in the explicit named
account store. An optional client secret can be supplied separately through
private local configuration. Do not run another writer against that same state
volume. Quota coordination is local to the selected state namespace; independent
hosts or containers with separate state do not share a quota ledger.

## Validation and history checks

```sh
python3 scripts/operations/validate-supervisors.py
twi history status --history "$HOME/.local/state/twi/history.sqlite3"
twi consume --channel yourchannel --last 10 --history "$HOME/.local/state/twi/history.sqlite3"
```

The validation script parses the plist, asks the actual systemd parser to validate
the user unit, and checks a sanitized Compose copy. It installs no service and
reads no `.env` file. For packaging signal/permission and real disk-exhaustion
checks, see [release verification](release.md) and
[history fault/performance evidence](history-performance.md).
