package twitch

import (
	"context"
	"time"
)

// FollowedChannel is one channel the authenticated user follows, as reported
// by Twitch Helix "Get Followed Channels" (sorted most-recently-followed
// first).
type FollowedChannel struct {
	BroadcasterID    string
	BroadcasterLogin string
	BroadcasterName  string
	FollowedAt       time.Time
}

// FollowedChannelLookup resolves the channels a user follows. Requires the
// user:read:follows scope, granted by the user for their own account.
type FollowedChannelLookup interface {
	GetFollowedChannels(ctx context.Context, userID string) ([]FollowedChannel, error)
}
