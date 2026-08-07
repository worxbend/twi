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
