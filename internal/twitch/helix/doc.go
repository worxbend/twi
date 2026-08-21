// Package helix implements twi's Twitch ports over the Helix HTTP API.
//
// Each adapter here -- ChannelsClient, ClipsClient, StreamsClient and the
// rest -- satisfies an interface declared in the parent twitch package, and
// nothing outside internal/cli refers to these types by name: the rest of twi
// holds the port, not the implementation, so a different backend or a fake
// substitutes without touching a line of it.
//
// The HTTP plumbing every adapter shares -- the client, the Client-Id and
// bearer headers, bounded response reads, and turning a non-2xx answer into a
// redacted, classified error -- lives once on transport rather than in each
// adapter.
package helix
