package twitch

import (
	"errors"
	"strings"

	irc "github.com/gempir/go-twitch-irc/v4"
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
// It exists so consumers outside this package can classify without importing
// the IRC library or knowing which layer produced the error: a failure raised
// by the transport carries irc.ErrLoginAuthenticationFailed, while one raised
// by this package carries ErrAuthFailed. Both mean the same thing to a caller
// deciding whether to tell someone their token is bad.
func IsAuthError(err error) bool {
	return err != nil &&
		(errors.Is(err, ErrAuthFailed) || errors.Is(err, irc.ErrLoginAuthenticationFailed))
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

// newSafeError wraps cause with a message safe to display. Callers are
// responsible for having redacted detail already.
func newSafeError(detail string, cause error) error {
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
