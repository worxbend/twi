package twitch

import (
	"context"
)

// UserLookup resolves Twitch user profile metadata through a Twitch API
// boundary. Implementations must batch IDs and logins instead of issuing one
// request per chat message.
type UserLookup interface {
	GetUsers(context.Context, UserLookupRequest) ([]UserIdentity, error)
}

// UserLookupRequest identifies Twitch users by ID and/or login. Twitch Helix
// accepts up to 100 combined identifiers per Get Users request.
type UserLookupRequest struct {
	UserIDs    []string
	UserLogins []string
}

// UserIdentity contains the user profile fields needed by avatar resolution.
type UserIdentity struct {
	UserID          string
	Login           string
	DisplayName     string
	ProfileImageURL string
}
