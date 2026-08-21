package twitch

import (
	"context"
	"time"
)

// Follower is one of a broadcaster's followers, as reported by Twitch Helix
// "Get Channel Followers" (sorted most-recently-followed first).
type Follower struct {
	UserID     string
	UserLogin  string
	UserName   string
	FollowedAt time.Time
}

// FollowersPage is one page of Twitch Helix "Get Channel Followers" results:
// the full follower count plus the most recent followers (for detecting new
// ones by polling and diffing, since Twitch only offers follow events
// through EventSub, not IRC or a webhook twi can receive).
type FollowersPage struct {
	Total     int
	Followers []Follower
}

// FollowerLookup resolves a broadcaster's follower count and most recent
// followers. Requires the moderator:read:followers scope, granted by the
// broadcaster to themselves (moderator_id == broadcaster_id).
type FollowerLookup interface {
	GetChannelFollowers(ctx context.Context, broadcasterID string, limit int) (FollowersPage, error)
}
