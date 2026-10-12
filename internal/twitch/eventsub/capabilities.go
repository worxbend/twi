package eventsub

import "github.com/worxbend/twi/internal/auth"

// MaxReadChannels respects 300 subscriptions with the seven read-scoped chat events.
const MaxReadChannels = 42
const MaxModerationChannels = 37

func readSubscriptions() []string {
	return []string{"channel.chat.message", "channel.chat.message_delete", "channel.chat.clear", "channel.chat.clear_user_messages", "channel.chat.notification", "channel.chat.user_message_hold", "channel.chat.user_message_update"}
}

// CanReadModeration checks scope alternatives documented for channel.moderate v2.
func CanReadModeration(scopes []auth.Scope) bool {
	have := map[auth.Scope]bool{}
	for _, scope := range scopes {
		have[scope] = true
	}
	for _, suffix := range []string{"blocked_terms", "chat_settings", "unban_requests", "banned_users", "chat_messages", "warnings"} {
		if !have[auth.Scope("moderator:read:"+suffix)] && !have[auth.Scope("moderator:manage:"+suffix)] {
			return false
		}
	}
	return have["moderator:read:moderators"] && have["moderator:read:vips"]
}
func (c *Client) subscriptions(channel Channel) []string {
	kinds := readSubscriptions()
	// A granted scope alone does not establish moderator status in arbitrary rooms.
	// Broadcaster identity proves the role; callers may also supply verified moderator rooms.
	if CanReadModeration(c.config.Scopes) {
		allowed := channel.ID == c.config.UserID
		for _, id := range c.config.ModeratorChannels {
			allowed = allowed || id == channel.ID
		}
		if allowed {
			kinds = append(kinds, "channel.moderate")
		}
	}
	return kinds
}
