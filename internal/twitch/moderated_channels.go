package twitch

import "context"

// ModeratedChannelLookup obtains current moderator-role proof for the token's
// user, restricted to selected broadcaster IDs. A scope grant is not role proof.
type ModeratedChannelLookup interface {
	GetModeratedChannels(context.Context, string, []string) ([]ModeratedChannel, error)
}
type ModeratedChannel struct{ BroadcasterID, BroadcasterLogin, BroadcasterName string }
