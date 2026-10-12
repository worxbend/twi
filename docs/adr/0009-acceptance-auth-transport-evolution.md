# 0009: Acknowledged sending and explicit reader transports

Status: accepted.

Automation sends use Helix acknowledgement fields, with sent/rejected/unknown/
invalid/validated outcomes. Ambiguous writes are not retried automatically. Local
send permits are shared per account and canonical channel across processes.
Capabilities request only read, send, or EventSub scopes. EventSub is explicitly
selected; IRC stays the compatibility reader. Both normalize public events and
report readiness/loss separately.

Consequences: queued IRC sends cannot claim Twitch acceptance. Reader disconnects
create gaps rather than invented replay. Shared chat and self-message behavior
still require credentialed validation before parity claims.
