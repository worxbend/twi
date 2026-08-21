package twitch

import (
	"context"
	"errors"
	"time"
)

// StreamMarker is one marked moment in a broadcaster's stream/VOD, created
// via Twitch Helix "Create Stream Marker" and listed via "Get Stream
// Markers".
type StreamMarker struct {
	ID              string
	CreatedAt       time.Time
	Description     string
	PositionSeconds int
	URL             string
}

// MarkerManager creates and lists Twitch stream markers for the
// broadcaster's own active stream. Creating a marker only succeeds while
// that broadcaster is live.
type MarkerManager interface {
	CreateStreamMarker(ctx context.Context, userID, description string) (StreamMarker, error)
	GetStreamMarkers(ctx context.Context, userID string, limit int) ([]StreamMarker, error)
}

// IsNoVideoFound reports whether err is a 404 from Get Stream Markers,
// Twitch's response when the broadcaster has no video/VOD at all yet (never
// streamed to completion, or VOD storage is disabled) - not a real failure,
// just "there are no markers because there's nothing to attach them to".
func IsNoVideoFound(err error) bool {
	var apiErr *ChannelAPIError
	return errors.As(err, &apiErr) && apiErr.Reason == ChannelAPINoVideoFound
}
