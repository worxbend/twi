package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/worxbend/twi/internal/twitch"
)

// resolveSelfBroadcasterID looks up the logged-in user's own Twitch user ID
// by login, the broadcaster_id every Stream Info and Misc tab Helix call
// needs. Shared so both tabs reuse one lookup instead of resolving (and
// caching, via shellModel.selfBroadcasterID) the ID twice.
func resolveSelfBroadcasterID(ctx context.Context, userLookup twitch.UserLookup, username string) (string, error) {
	username = strings.TrimSpace(username)
	if userLookup == nil || username == "" {
		return "", fmt.Errorf("resolve your Twitch user ID: missing username or user lookup")
	}
	users, err := userLookup.GetUsers(ctx, twitch.UserLookupRequest{UserLogins: []string{username}})
	if err != nil {
		return "", err
	}
	for _, u := range users {
		if strings.EqualFold(u.Login, username) {
			return u.UserID, nil
		}
	}
	return "", fmt.Errorf("could not resolve a Twitch user ID for %q", username)
}

// resolveBroadcasterID returns knownID as-is when it's already set, and
// otherwise resolves it via resolveSelfBroadcasterID. Callers use this to
// avoid repeating the "use the cached ID or look it up" check at every Helix
// call site.
func resolveBroadcasterID(ctx context.Context, userLookup twitch.UserLookup, username, knownID string) (string, error) {
	if knownID != "" {
		return knownID, nil
	}
	return resolveSelfBroadcasterID(ctx, userLookup, username)
}
