package auth

import "strings"

// Scope is a Twitch OAuth scope requested or granted during login.
type Scope string

const (
	// ScopeChatRead allows Twitch IRC clients to receive chat messages.
	ScopeChatRead Scope = "chat:read"
	// ScopeChatEdit allows Twitch IRC clients to send chat messages.
	ScopeChatEdit Scope = "chat:edit"
	// ScopeChannelManageBroadcast allows reading and updating the
	// authenticated broadcaster's channel info (title, category, language,
	// tags) through Twitch Helix "Get/Modify Channel Information", and
	// creating/listing stream markers.
	ScopeChannelManageBroadcast Scope = "channel:manage:broadcast"
	// ScopeModeratorReadFollowers allows reading the authenticated
	// broadcaster's own follower list/count through Twitch Helix "Get
	// Channel Followers" (moderator_id set to the broadcaster's own ID).
	ScopeModeratorReadFollowers Scope = "moderator:read:followers"
	// ScopeChannelReadSubscriptions allows reading the authenticated
	// broadcaster's subscriber count through Twitch Helix "Get Broadcaster
	// Subscriptions".
	ScopeChannelReadSubscriptions Scope = "channel:read:subscriptions"
	// ScopeClipsEdit allows creating a clip of the authenticated
	// broadcaster's active stream through Twitch Helix "Create Clip", used
	// by the /clip chat command.
	ScopeClipsEdit Scope = "clips:edit"
	// ScopeUserReadFollows allows reading the channels the authenticated
	// user follows through Twitch Helix "Get Followed Channels", used to
	// autocomplete the /channels picker.
	ScopeUserReadFollows Scope = "user:read:follows"
)

var requiredChatScopes = []Scope{ScopeChatRead, ScopeChatEdit}

var streamManageScopes = []Scope{ScopeChannelManageBroadcast}

var channelMetricsScopes = []Scope{ScopeModeratorReadFollowers, ScopeChannelReadSubscriptions}

var clipScopes = []Scope{ScopeClipsEdit}

var followedChannelScopes = []Scope{ScopeUserReadFollows}

// RequiredChatScopes returns the minimum OAuth scopes for twi's MVP chat read
// and send behavior.
func RequiredChatScopes() []Scope {
	return append([]Scope(nil), requiredChatScopes...)
}

// StreamManageScopes returns the OAuth scopes required to view and edit the
// broadcaster's own stream info (title, category, language, tags) on the
// Stream Info tab.
func StreamManageScopes() []Scope {
	return append([]Scope(nil), streamManageScopes...)
}

// ChannelMetricsScopes returns the OAuth scopes required to show follower
// and subscriber counts in the chat status line.
func ChannelMetricsScopes() []Scope {
	return append([]Scope(nil), channelMetricsScopes...)
}

// ClipScopes returns the OAuth scopes required for the /clip chat command.
func ClipScopes() []Scope {
	return append([]Scope(nil), clipScopes...)
}

// FollowedChannelScopes returns the OAuth scopes required to autocomplete
// the /channels picker from the channels the user follows.
func FollowedChannelScopes() []Scope {
	return append([]Scope(nil), followedChannelScopes...)
}

// LoginScopes returns every OAuth scope `twi login` requests: the required
// chat read/send scopes, stream info management for the Stream Info and
// Misc tabs, the channel metrics scopes for the status line's
// follower/subscriber counts, clip creation for the /clip command, and the
// follow list backing the /channels picker.
func LoginScopes() []Scope {
	scopes := RequiredChatScopes()
	scopes = append(scopes, StreamManageScopes()...)
	scopes = append(scopes, ChannelMetricsScopes()...)
	scopes = append(scopes, ClipScopes()...)
	scopes = append(scopes, FollowedChannelScopes()...)
	return scopes
}

// MissingScopes returns required scopes that are absent from granted.
func MissingScopes(granted, required []Scope) []Scope {
	have := make(map[Scope]bool, len(granted))
	for _, scope := range granted {
		have[scope] = true
	}

	missing := make([]Scope, 0, len(required))
	for _, scope := range required {
		if !have[scope] {
			missing = append(missing, scope)
		}
	}
	return missing
}

// Scopes converts non-empty string values into Scope values.
func Scopes(values ...string) []Scope {
	scopes := make([]Scope, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			scopes = append(scopes, Scope(value))
		}
	}
	return scopes
}

// ScopeValues converts scopes into their string OAuth values.
func ScopeValues(scopes []Scope) []string {
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, string(scope))
	}
	return values
}
