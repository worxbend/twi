package twitch

import (
	"context"
	"time"
)

// StreamInfo reports Twitch Helix "Get Streams" broadcast status for one
// channel. A channel absent from the Helix response is offline: Live is
// false and StartedAt/ViewerCount are zero.
type StreamInfo struct {
	UserLogin   string
	Live        bool
	StartedAt   time.Time
	ViewerCount int
}

// StreamLookup resolves live broadcast status for a batch of channel logins.
type StreamLookup interface {
	GetStreams(ctx context.Context, logins []string) ([]StreamInfo, error)
}
