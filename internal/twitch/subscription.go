package twitch

import (
	"context"
)

// SubscriptionsPage reports a broadcaster's subscriber count from Twitch
// Helix "Get Broadcaster Subscriptions". Points is Twitch's weighted total
// (Tier 2/3 subs count as more than one point); Total is the plain
// subscriber count.
type SubscriptionsPage struct {
	Total  int
	Points int
}

// SubscriptionLookup resolves a broadcaster's subscriber count. Requires the
// channel:read:subscriptions scope.
type SubscriptionLookup interface {
	GetBroadcasterSubscriptions(ctx context.Context, broadcasterID string, limit int) (SubscriptionsPage, error)
}
