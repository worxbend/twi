# Sending transports and application authorization

`produce` defaults to acknowledged Helix sending with a user token. The sender
identity comes from validation, and the token needs `user:write:chat`. Replies,
JSONL routing, input order, conservative shared quotas, and uncertain outcomes
keep their existing contracts.

## Explicit application-token sending

```sh
twi produce --auth app --sender your_bot --shared-chat source \
  --channel your_channel --message 'An acknowledged bot message'
```

This mode obtains a client-credentials app token using the configured Twitch
client ID and client secret. It validates the token's application identity,
rejects a user token in that role, keeps the app token in memory, revalidates
hourly, and obtains a new app token when needed. It never falls back to an
existing environment or saved user token. No token is printed or placed in an
argument. Named `--account` selection can supply the application's client ID;
its saved user access token is not used to authorize the send.

Twitch's [Send Chat Message](https://dev.twitch.tv/docs/api/reference/#send-chat-message)
requires the sending user to have authorized the application for `user:write:chat`
and `user:bot`. The broadcaster must also authorize `channel:bot`, unless the
sender has currently verified moderator status in that room. The producer checks
[application grants](https://dev.twitch.tv/docs/api/reference/#get-authorization-by-user)
before each send. Where the sender granted `user:read:moderated_channels`, it can
verify the moderator alternative through Get Moderated Channels. A scope grant
alone never establishes a moderator role.

Use `login --capability bot-send` for the sending account and
`login --capability channel-bot` for the broadcaster. The comprehensive
`validation` capability also includes those scopes for controlled tests. The
application's registered client ID must match both grants. The broadcaster and
sender may be different accounts; `--sender` is always explicit.

App mode sets `for_source_only=true` by default, including an explicit
`--shared-chat source`. `--shared-chat all` explicitly sets it to false and permits
shared-session propagation. User-token mode never sends that app-only parameter:
Twitch controls shared-session propagation for user tokens, so selecting one
channel does not promise visibility stays in that channel. Receipts identify
`authorization`, `transport`, and app-mode `for_source_only` without exposing
message text or credentials.

## Explicit IRC action messages

Twitch's documented Helix send request has no action-message field. The CLI does
not invent an acknowledged `/me` request or switch transports automatically.
Select the documented [IRC action transport](https://dev.twitch.tv/docs/chat/irc/)
explicitly:

```sh
twi produce --transport irc --action --channel your_channel --message 'waves hello'
```

IRC requires `chat:read` and `chat:edit` for authenticated startup and channel
readiness. The producer waits for registration and room readiness, uses the
shared authentication/join/channel/send quotas, and completes each TCP/TLS write
before emitting its receipt. It does not use the interactive client's asynchronous
send queue, so EOF cannot discard a queued final message.

An IRC receipt has `status="unconfirmed"` and an empty `message_id`. A successful
socket write proves neither Twitch acceptance nor permanent visibility. A socket
write failure can be ambiguous and never automatically retries. Observe through
a separate recorder when you need received-message evidence. No locally invented
Twitch message ID is emitted.

For JSONL, set `"action":true` per record and select `--transport irc`; the global
`--action` flag applies only to lines input. The producer preserves meaningful
spaces and rejects invalid UTF-8, forbidden controls, and oversized input rather
than trimming or splitting. Action bodies are conservatively limited to 491
characters so the required nine-character CTCP framing fits the 500-character
wire bound. Ordinary automation messages retain the 500-character validation.
IRC replies use the same selected channel and pacing; reply IDs cannot contain
protocol tag separators or whitespace.

`--auth app` is incompatible with IRC. Helix action requests are rejected before
network use; app-only sender/shared-chat options are rejected in user mode.
`--dry-run` validates routing and options without opening sockets or obtaining
credentials, and reports `validated` rather than `sent` or `unconfirmed`.
