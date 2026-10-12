# Automation schemas

JSONL records use explicit schema version 1 DTOs. Each object ends with LF.
Output consumers should tolerate additive unknown fields. Strict producer input
rejects unknown fields. A missing input channel is permitted only when one
channel default was explicitly supplied.

- [Events/messages](event-v1.schema.json)
- [Send results](send-result-v1.schema.json)
- [Diagnostics](diagnostic-v1.schema.json)
- [History status](history-status-v1.schema.json)
- [History prune](history-prune-v1.schema.json)
- [Producer input](producer-input-v1.schema.json)
- [Example records](fixtures.jsonl)

IDs are strings. Live cursors are null. Local sequence/store cursor, Twitch
message ID, EventSub delivery ID, and producer correlation ID are distinct.
Receipts omit input text. Optional source-channel/source-message fields describe
shared-chat observation separately from the observed channel.
