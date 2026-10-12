package eventsub

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

// actionPayload deliberately excludes message_body and arbitrary extra fields.
type actionPayload struct {
	UserID           string    `json:"user_id"`
	UserLogin        string    `json:"user_login"`
	UserName         string    `json:"user_name"`
	MessageID        string    `json:"message_id"`
	Reason           string    `json:"reason"`
	Expires          time.Time `json:"expires_at"`
	FollowMinutes    *int      `json:"follow_duration_minutes"`
	WaitSeconds      *int      `json:"wait_time_seconds"`
	Viewers          *int      `json:"viewer_count"`
	Approved         *bool     `json:"is_approved"`
	ModeratorMessage string    `json:"moderator_message"`
	TermsAction      string    `json:"action"`
	TermsList        string    `json:"list"`
	Terms            []string  `json:"terms"`
	FromAutomod      *bool     `json:"from_automod"`
	CitedRules       []string  `json:"chat_rules_cited"`
}

func normalizeModeration(frame envelope) (twitch.Event, bool, error) {
	var base struct {
		BroadcasterID    string `json:"broadcaster_user_id"`
		BroadcasterLogin string `json:"broadcaster_user_login"`
		ModeratorID      string `json:"moderator_user_id"`
		ModeratorLogin   string `json:"moderator_user_login"`
		ModeratorName    string `json:"moderator_user_name"`
		Action           string `json:"action"`
		SourceID         string `json:"source_broadcaster_user_id"`
		SourceLogin      string `json:"source_broadcaster_user_login"`
	}
	if err := json.Unmarshal(frame.Payload.Event, &base); err != nil {
		return twitch.Event{}, true, errors.New("invalid EventSub moderation notification")
	}
	if base.BroadcasterLogin == "" || base.Action == "" {
		return twitch.Event{}, true, errors.New("moderation notification missing identity")
	}
	event := twitch.ModerationEvent{Type: twitch.ModerationAction, Action: base.Action, ModeratorID: base.ModeratorID, ModeratorLogin: base.ModeratorLogin, ModeratorName: base.ModeratorName, SourceChannelID: base.SourceID, SourceChannelLogin: base.SourceLogin, DeliveryID: frame.Metadata.ID, Channel: base.BroadcasterLogin, RoomID: base.BroadcasterID, Timestamp: frame.Metadata.At, Parameters: map[string]string{}}
	field := ""
	switch base.Action {
	case "ban", "timeout", "unban", "untimeout", "vip", "unvip", "mod", "unmod", "raid", "unraid", "delete", "warn", "followers", "slow", "shared_chat_ban", "shared_chat_timeout", "shared_chat_unban", "shared_chat_untimeout", "shared_chat_delete":
		field = base.Action
	case "add_blocked_term", "add_permitted_term", "remove_blocked_term", "remove_permitted_term":
		field = "automod_terms"
	case "approve_unban_request", "deny_unban_request":
		field = "unban_request"
	}
	if field != "" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(frame.Payload.Event, &fields); err != nil {
			return twitch.Event{}, true, errors.New("invalid moderation payload")
		}
		var details actionPayload
		if raw := fields[field]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &details); err != nil {
				return twitch.Event{}, true, errors.New("invalid moderation action details")
			}
		}
		event.TargetUserID = details.UserID
		event.TargetLogin = details.UserLogin
		event.TargetName = details.UserName
		event.TargetMessageID = details.MessageID
		event.Reason = details.Reason
		event.ExpiresAt = details.Expires
		if !details.Expires.IsZero() {
			event.BanDuration = max(time.Duration(0), details.Expires.Sub(frame.Metadata.At))
		}
		ints := []struct {
			name  string
			value *int
		}{{"follow_duration_minutes", details.FollowMinutes}, {"wait_time_seconds", details.WaitSeconds}, {"viewer_count", details.Viewers}}
		for _, item := range ints {
			if item.value != nil {
				event.Parameters[item.name] = strconv.Itoa(*item.value)
			}
		}
		if details.Approved != nil {
			event.Parameters["is_approved"] = strconv.FormatBool(*details.Approved)
		}
		if details.FromAutomod != nil {
			event.Parameters["from_automod"] = strconv.FormatBool(*details.FromAutomod)
		}
		stringsToCopy := map[string]string{"moderator_message": details.ModeratorMessage, "terms_action": details.TermsAction, "terms_list": details.TermsList}
		for key, value := range stringsToCopy {
			if len(value) > 2048 {
				return twitch.Event{}, true, errors.New("moderation parameter exceeds bound")
			}
			if value != "" {
				event.Parameters[key] = value
			}
		}
		if len(details.Terms) > 64 || len(details.CitedRules) > 64 {
			return twitch.Event{}, true, errors.New("moderation parameter list exceeds bound")
		}
		for _, list := range []struct {
			name   string
			values []string
		}{{"term", details.Terms}, {"cited_rule", details.CitedRules}} {
			for index, value := range list.values {
				if len(value) > 2048 {
					return twitch.Event{}, true, errors.New("moderation parameter exceeds bound")
				}
				event.Parameters[list.name+"_"+strconv.Itoa(index)] = value
			}
		}
	}
	// Shared-room actions retain their target metadata as auxiliary events. Only
	// observed-room clear/delete subscriptions authorize privacy changes here.
	if !strings.HasPrefix(base.Action, "shared_chat_") && (base.SourceID == "" || base.SourceID == base.BroadcasterID) {
		switch base.Action {
		case "ban":
			event.Type = twitch.ModerationUserBanned
		case "timeout":
			event.Type = twitch.ModerationUserTimedOut
		case "delete":
			event.Type = twitch.ModerationMessageDeleted
		case "clear":
			event.Type = twitch.ModerationChatCleared
		}
	}
	return twitch.Event{Kind: twitch.EventModeration, Moderation: event}, true, nil
}
