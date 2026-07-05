// Package assets resolves chat asset metadata into safe cache events.
//
// It handles avatar identity lookups, Twitch emote and badge metadata, emoji
// provider metadata, public image downloading, cache write-through, and
// redacted asset events. Public downloads are validated and stored through
// URL-free cache identities so debug output and renderer state do not expose
// private or credential-shaped source values.
package assets
