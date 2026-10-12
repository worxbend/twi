# Authorize a controlled live validation session

`twi` uses Twitch's documented OAuth, IRC, Helix, and EventSub interfaces. Local
history is your own previously captured data. No third-party message provider is
used.

Register your own Twitch application and its exact localhost callback using
[register-twitch-app.md](register-twitch-app.md). Supply its client ID and client
secret through your private environment or config. The login command saves tokens
through the private credential store; it does not print them.

For a controlled broadcaster channel, create an explicit local account with the
validation capabilities:

```sh
twi login --device --account validation --capability validation --timeout 30m
```

Open the displayed Twitch activation URL in a browser, sign in to the intended
account, and approve the requested scopes. The command saves the grant privately.
For a registered localhost callback, omit `--device` to use authorization-code login. The `validation` capability
includes IRC reading/sending, EventSub chat reading, acknowledged API sending,
moderation-event reading, verified moderated-channel discovery, and chat-message deletion. It is intended for acceptance
testing in a channel where the authenticated account has the corresponding
broadcaster/moderator rights. A granted scope alone does not establish a moderator
role in another channel.

Inspect saved identity and granted capabilities without exposing credentials:

```sh
twi account show validation
twi doctor --account validation
```

Replace `your_channel` below with the controlled Twitch channel. These are live
commands; the producer sends the provided text to Twitch.

```sh
twi record --account validation --transport eventsub --channel your_channel \
  --history "$HOME/.local/state/twi/history.sqlite3"

# In another terminal:
twi produce --account validation --channel your_channel --message 'twi acceptance check'
twi consume --channel your_channel --history "$HOME/.local/state/twi/history.sqlite3" --last 1
twi chat --channel your_channel --history "$HOME/.local/state/twi/history.sqlite3"
```

The history terminal attaches read-only to the running recorder. Add
`--account validation --allow-send` when you explicitly want its composer to send
through Helix. The sender reports Twitch acknowledgements and uses the shared
quota service. An ambiguous write never automatically resends. Native IRC `/me`
actions remain available; the acknowledged Helix composer currently rejects that
action explicitly.

To exercise the live terminal directly:

```sh
twi chat --account validation --transport eventsub --channel your_channel --allow-send
```

For IRC compatibility, select `--transport irc`; its normal composer reports an
unconfirmed enqueue. Add `--allow-send` to select acknowledged Helix sends.

Use the narrower `eventsub-read`, `chat-read`, or `chat-send` login capabilities
when only that operation is needed. Each login obtains a new scope grant; do not
assume a read-only token acquired earlier also permits sending or moderation.
Named accounts keep those grants separate and never fall back to a default token
when the selected account is missing.

Live validation still depends on Twitch's current authorization and channel
behavior. Offline adapter tests do not establish live acceptance, shared-chat
visibility, moderation coverage, or Unicode limits. Record actual observations
and outstanding cases in [manual-validation.md](manual-validation.md).

## Verified moderator discovery

The `moderation-read` and `validation` grants include
`user:read:moderated_channels`. For non-owned selected rooms, the EventSub factory
queries Twitch's [Get Moderated Channels](https://dev.twitch.tv/docs/api/reference/#get-moderated-channels)
endpoint using the authenticated token's user ID. It adds `channel.moderate` only
for selected IDs that Twitch currently confirms, and only when the complete
moderation-event scope set is also granted. An optional role lookup failure
stops that enhanced subscription setup rather than claiming complete moderation
coverage. Without the discovery scope, basic chat events remain available; full
moderation events are subscribed only for the authenticated broadcaster's own
channel when its moderation scopes permit them.

Acknowledged producers use the same endpoint for fresh moderator quota proof.
Missing scope, negative proof, or lookup failure keeps conservative normal-user
pacing. Own-channel broadcaster identity is independently verifiable. No command
line flag declares moderator or VIP status, and no scope grant alone raises a
send quota. Pagination and requests remain bounded and cancellable.
