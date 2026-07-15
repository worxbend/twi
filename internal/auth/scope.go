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
	// tags) through Twitch Helix "Get/Modify Channel Information".
	ScopeChannelManageBroadcast Scope = "channel:manage:broadcast"
)

var requiredChatScopes = []Scope{ScopeChatRead, ScopeChatEdit}

var streamManageScopes = []Scope{ScopeChannelManageBroadcast}

// ChatReadScopes returns the OAuth scopes required for read-only Twitch chat.
func ChatReadScopes() []Scope {
	return []Scope{ScopeChatRead}
}

// ChatSendScopes returns the OAuth scopes required to send Twitch chat messages.
func ChatSendScopes() []Scope {
	return []Scope{ScopeChatEdit}
}

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

// LoginScopes returns every OAuth scope `twi login` requests: the required
// chat read/send scopes plus stream info management for the Stream Info tab.
func LoginScopes() []Scope {
	return append(RequiredChatScopes(), StreamManageScopes()...)
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
