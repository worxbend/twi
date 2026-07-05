// Package auth contains OAuth login, token, scope, and secret primitives.
//
// Secret values are wrapped so ordinary formatting and JSON encoding redact
// them. Callers must use explicit reveal paths only at the boundary that sends
// credentials to Twitch or writes them through the storage layer.
package auth
