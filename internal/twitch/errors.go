package twitch

import (
	"errors"
)

// ErrAuthFailed marks a failure Twitch attributed to the credentials rather
// than to the network, the server, or the request.
//
// It exists so consumers can ask errors.Is instead of searching the message
// text. Substring matching on "auth" is what the app used to do, and it
// classified "x509: certificate signed by unknown authority" -- a TLS trust
// problem -- as a bad OAuth token, sending anyone who hit it to re-run a login
// that was never the issue.
var ErrAuthFailed = errors.New("twitch authentication failed")

// IsAuthError reports whether err represents rejected credentials.
//
// Transports are responsible for joining ErrAuthFailed onto any failure
// Twitch attributed to the credentials, so this asks one question rather than
// enumerating the sentinel each transport library happens to use. That is
// what keeps this package -- the domain model the UI is written against --
// free of any dependency on how twi actually talks to Twitch.
func IsAuthError(err error) bool {
	return err != nil && errors.Is(err, ErrAuthFailed)
}

// safeError reports a redacted message while keeping the original error
// reachable through errors.Is and errors.As.
//
// The transport must never print raw error text, because tokens appear in it.
// The previous approach -- errors.New(redact(err.Error())) -- achieved that by
// throwing the cause away, which left callers with nothing but a string to
// classify by and silently broke every errors.Is check downstream of it.
// Redacting the message and preserving the chain gives both.
type safeError struct {
	detail string
	cause  error
}

func (e *safeError) Error() string { return e.detail }

func (e *safeError) Unwrap() error { return e.cause }

// NewSafeError wraps cause with a message safe to display, keeping the
// original reachable through errors.Is and errors.As.
//
// It is exported because the transports that produce credential-bearing
// errors -- the IRC client, the Helix adapters -- live outside this package
// and must not print raw error text. Callers are responsible for having
// redacted detail already.
func NewSafeError(detail string, cause error) error {
	if cause == nil {
		return nil
	}
	return &safeError{detail: detail, cause: cause}
}

// EventDropCounter is implemented by transports that discard events when a
// consumer falls behind. Dropping is preferable to blocking -- a stalled
// consumer must never wedge the goroutine that answers Twitch's PING -- but
// the count has to reach the UI, because silently losing chat is not
// something a moderator should have to discover for themselves.
type EventDropCounter interface {
	DroppedEvents() uint64
}

// ErrNotConnected reports a send attempted while the IRC session is not
// registered. It is not an auth failure and must not be reported as one.
var ErrNotConnected = errors.New("not connected to Twitch IRC; the message was not sent")

// ErrRateLimited reports a send refused locally because it would exceed
// Twitch's chat allowance. Exceeding it gets the connection closed, not just
// the message rejected, so twi declines rather than finding out from Twitch.
var ErrRateLimited = errors.New("sending too fast; Twitch limits chat to 20 messages per 30 seconds")

// ErrDuplicateMessage reports a message identical to one just sent in the
// same channel, which Twitch rejects with msg_duplicate.
var ErrDuplicateMessage = errors.New("identical to your last message; Twitch rejects repeats, so change it slightly")
