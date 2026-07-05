// Package app owns the terminal UI model and app-facing chat boundary.
//
// It contains the Bubble Tea update/view state, per-channel chat state,
// command palette, local filters, inspect/reply behavior, fake chat client, and
// live-client adapter. The package consumes normalized Twitch messages and
// internal interfaces; it must not depend on concrete Twitch transport types or
// perform blocking network or filesystem work from View.
package app
