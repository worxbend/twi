// Package debuglog writes opt-in JSON-line diagnostics with explicit redaction.
//
// Callers provide curated fields instead of raw structs. This keeps transport,
// auth, config, storage, asset, and render diagnostics useful without exposing
// OAuth tokens, refresh tokens, client secrets, callback values, or private
// source URLs.
package debuglog
