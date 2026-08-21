// Package irc implements twi's ChatClient port over Twitch's IRC interface.
//
// It owns everything about the wire: connecting and reconnecting, refreshing
// an OAuth token that expires mid-session, Twitch's chat rate limits, and
// sanitizing outbound text. Its other half is normalization -- turning the
// callback structs the underlying library delivers into the Events and
// ChatMessages declared in the parent twitch package, so nothing above this
// package ever sees a third-party chat type.
//
// The library is imported as gempir throughout, because this package is
// itself called irc and the two are easy to confuse otherwise.
package irc
