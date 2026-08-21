// Package twitch is twi's model of Twitch chat: the things a chat client
// deals in, and the interfaces through which it reaches them.
//
// It holds the entities -- a ChatMessage and its badges, emotes and
// fragments; the Events a chat session produces; a ChannelInfo, Clip, Game,
// StreamMarker, Follower and the rest -- together with the ports that fetch
// and act on them: ChatClient, ChannelManager, ClipManager, StreamLookup,
// TokenValidator and their siblings.
//
// It deliberately contains no way of talking to Twitch. There is no HTTP
// client here, no IRC library, and no OAuth exchange; this package does not
// import any of them, and a test enforces that. Those live in adapters
// beside it:
//
//   - twitch/helix implements the lookup and management ports over the
//     Twitch Helix HTTP API.
//   - twitch/irc implements ChatClient over Twitch's IRC interface, and
//     normalizes what the wire delivers into the Events above.
//
// Dependencies point inward: the adapters import this package, never the
// other way round, and internal/cli is the only place that knows which
// adapter is wired to which port. That is what lets internal/render and
// internal/app -- which draw chat and drive the UI -- be written against the
// model alone, compile without any of Twitch's transport machinery, and be
// tested without a network.
//
// Failures cross the boundary already classified. An adapter that learns
// Twitch rejected the credentials joins ErrAuthFailed onto the error, and one
// that learns a channel-scoped call failed reports a ChannelAPIReason, so
// callers ask IsAuthError or IsMissingScope rather than matching on message
// text or on an HTTP status code this package never sees.
package twitch
