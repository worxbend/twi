package helix

import "strings"

// resolveTokenSource turns a config's optional token provider and static token
// into a single accessor read at request time.
//
// Helix clients used to copy the OAuth token into a field at construction. A
// mid-session refresh rotates that token, so every client built at startup
// carried on presenting one Twitch had already invalidated: the LIVE
// indicator, follower and subscriber counts, /clip, stream markers, Stream
// Info and the emote index all began failing with 401 while chat, which does
// refresh, kept working. Reading through a provider keeps them current.
func resolveTokenSource(source func() string, static string) func() string {
	if source != nil {
		return func() string { return strings.TrimSpace(source()) }
	}
	trimmed := strings.TrimSpace(static)
	return func() string { return trimmed }
}
