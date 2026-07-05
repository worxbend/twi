// Package twitch owns Twitch protocol and API integration.
//
// It wraps IRC transport, OAuth token validation, Helix user metadata, Twitch
// emote and badge metadata, and event normalization. Package consumers receive
// twi-owned message and state types rather than concrete callback structs from
// third-party Twitch clients.
package twitch
