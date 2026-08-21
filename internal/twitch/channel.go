package twitch

import (
	"context"
	"errors"
)

// ChannelInfo reports Twitch Helix "Get Channel Information" fields for one
// broadcaster.
type ChannelInfo struct {
	BroadcasterID    string
	BroadcasterLogin string
	BroadcasterName  string
	GameID           string
	GameName         string
	Title            string
	Language         string
	Tags             []string
}

// ChannelInfoUpdate describes a Twitch Helix "Modify Channel Information"
// request. Only non-nil fields are sent, so unrelated channel info is left
// untouched; Tags, when non-nil, replaces the full tag list (Twitch has no
// partial-tag-update endpoint).
type ChannelInfoUpdate struct {
	Title    *string
	GameID   *string
	Language *string
	Tags     *[]string
}

// IsEmpty reports whether the update has no fields set, so callers can skip
// issuing a no-op PATCH request.
func (u ChannelInfoUpdate) IsEmpty() bool {
	return u.Title == nil && u.GameID == nil && u.Language == nil && u.Tags == nil
}

// ChannelManager resolves and updates a broadcaster's own channel info
// through Twitch Helix. Implementations must not perform network work from a
// Bubble Tea View.
type ChannelManager interface {
	GetChannelInformation(ctx context.Context, broadcasterID string) (ChannelInfo, error)
	ModifyChannelInformation(ctx context.Context, broadcasterID string, update ChannelInfoUpdate) error
}

// ChannelAPIError wraps a Get/Modify Channel Information failure with the
// HTTP status Twitch returned, so callers can distinguish "missing scope or
// unauthorized token" (401) from other failures without parsing message
// text. Twitch returns 401 for both an expired/invalid token and a token
// missing channel:manage:broadcast (or the older equivalent scopes), so
// IsMissingScope treats either case as "re-run `twi login`."
type ChannelAPIError struct {
	// Reason is why the call failed, in terms the rest of twi can act on.
	Reason ChannelAPIReason
	// StatusCode is the HTTP status Twitch answered with, kept for
	// diagnostics and the debug log. Nothing outside the adapter that
	// created this error should branch on it -- branch on Reason, which is
	// what the adapter classified it into.
	StatusCode int
	err        error
}

// ChannelAPIReason says why a channel-scoped Twitch API call failed.
//
// The adapter translates whatever the transport reported into one of these,
// so the rest of twi can decide what to tell someone without knowing that
// Twitch speaks HTTP or which of its status codes means what.
type ChannelAPIReason int

const (
	// ChannelAPIFailed is a failure with no more specific meaning.
	ChannelAPIFailed ChannelAPIReason = iota
	// ChannelAPIMissingScope means Twitch rejected the credentials: the
	// token has expired, or was never granted the scope the call needs.
	// Both are fixed by running `twi login` again.
	ChannelAPIMissingScope
	// ChannelAPINoVideoFound means there is no broadcast to act on -- the
	// channel is not live, or has no video for a marker to attach to. It is
	// a normal state, not a misconfiguration.
	ChannelAPINoVideoFound
)

// NewChannelAPIError wraps err as a channel API failure with the reason the
// adapter classified it as, and the HTTP status it came from for diagnostics.
//
// Classifying at the boundary is what lets callers tell "your token is
// missing a scope" from "that broadcaster is not live" without either
// matching on message text or learning Twitch's status codes. The wrapped
// error stays reachable through errors.Is and errors.As.
func NewChannelAPIError(reason ChannelAPIReason, statusCode int, err error) error {
	return &ChannelAPIError{Reason: reason, StatusCode: statusCode, err: err}
}

// Error and Unwrap tolerate a missing cause. Reason and StatusCode are
// exported, so a zero-valued ChannelAPIError can be built by a struct literal
// that skips the constructor, and an error value that panics when printed is
// far worse than one that reports little.
func (e *ChannelAPIError) Error() string {
	if e == nil || e.err == nil {
		return "twitch channel API call failed"
	}
	return e.err.Error()
}

func (e *ChannelAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// IsMissingScope reports whether Twitch rejected the call's credentials - in
// practice always a token that has expired or lacks the scope the call needs,
// both of which are fixed by running `twi login` again.
func IsMissingScope(err error) bool {
	var apiErr *ChannelAPIError
	return errors.As(err, &apiErr) && apiErr.Reason == ChannelAPIMissingScope
}
