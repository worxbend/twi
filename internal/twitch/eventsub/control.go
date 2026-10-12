package eventsub

import (
	"encoding/json"
	"errors"
	"github.com/worxbend/twi/internal/twitch"
	"strconv"
)

func normalizeControl(frame envelope) (twitch.Event, bool, error) {
	switch frame.Metadata.Subscription {
	case "channel.chat.notification":
		var data struct {
			BroadcasterID    string      `json:"broadcaster_user_id"`
			BroadcasterLogin string      `json:"broadcaster_user_login"`
			ChatterID        string      `json:"chatter_user_id"`
			ChatterLogin     string      `json:"chatter_user_login"`
			ChatterName      string      `json:"chatter_user_name"`
			MessageID        string      `json:"message_id"`
			NoticeType       string      `json:"notice_type"`
			System           string      `json:"system_message"`
			Color            string      `json:"color"`
			SourceID         string      `json:"source_broadcaster_user_id"`
			SourceLogin      string      `json:"source_broadcaster_user_login"`
			SourceMessageID  string      `json:"source_message_id"`
			Message          chatBody    `json:"message"`
			Badges           []chatBadge `json:"badges"`
			SourceBadges     []chatBadge `json:"source_badges"`
			Raid             struct {
				UserID    string `json:"user_id"`
				UserLogin string `json:"user_login"`
				Viewers   int    `json:"viewer_count"`
			} `json:"raid"`
			Sub struct {
				Tier   string `json:"sub_tier"`
				Prime  bool   `json:"is_prime"`
				Months int    `json:"duration_months"`
			} `json:"sub"`
			Resub struct {
				Tier   string `json:"sub_tier"`
				Months int    `json:"cumulative_months"`
			} `json:"resub"`
			Gift struct {
				RecipientID    string `json:"recipient_user_id"`
				RecipientLogin string `json:"recipient_user_login"`
				Tier           string `json:"sub_tier"`
			} `json:"sub_gift"`
		}
		if err := json.Unmarshal(frame.Payload.Event, &data); err != nil {
			return twitch.Event{}, true, errors.New("invalid EventSub chat notification")
		}
		if data.BroadcasterLogin == "" || data.NoticeType == "" {
			return twitch.Event{}, true, errors.New("EventSub notification missing channel or type")
		}
		notice := twitch.UserNotice{DeliveryID: frame.Metadata.ID, SourceChannelID: data.SourceID, SourceChannelLogin: data.SourceLogin, SourceMessageID: data.SourceMessageID, ID: data.MessageID, MessageID: data.NoticeType, Channel: data.BroadcasterLogin, RoomID: data.BroadcasterID, Timestamp: frame.Metadata.At, AuthorID: data.ChatterID, AuthorLogin: data.ChatterLogin, DisplayName: data.ChatterName, AuthorColor: data.Color, Text: data.Message.Text, SystemText: data.System, Params: map[string]string{}}
		notice.Fragments = normalizeFragments(data.Message.Fragments)
		notice.Badges = normalizeBadges(data.Badges)
		notice.SourceBadges = normalizeBadges(data.SourceBadges)
		switch data.NoticeType {
		case "raid":
			notice.Params["viewer_count"] = strconv.Itoa(data.Raid.Viewers)
			notice.Params["raider_user_id"] = data.Raid.UserID
			notice.Params["raider_login"] = data.Raid.UserLogin
		case "sub":
			notice.Params["sub_tier"] = data.Sub.Tier
			notice.Params["months"] = strconv.Itoa(data.Sub.Months)
			notice.Params["is_prime"] = strconv.FormatBool(data.Sub.Prime)
		case "resub":
			notice.Params["sub_tier"] = data.Resub.Tier
			notice.Params["months"] = strconv.Itoa(data.Resub.Months)
		case "sub_gift":
			notice.Params["recipient_user_id"] = data.Gift.RecipientID
			notice.Params["recipient_login"] = data.Gift.RecipientLogin
			notice.Params["sub_tier"] = data.Gift.Tier
		}
		return twitch.Event{Kind: twitch.EventUserNotice, UserNotice: notice}, true, nil
	case "channel.chat.user_message_hold", "channel.chat.user_message_update":
		var data struct {
			BroadcasterID    string `json:"broadcaster_user_id"`
			BroadcasterLogin string `json:"broadcaster_user_login"`
			UserID           string `json:"user_id"`
			MessageID        string `json:"message_id"`
			Status           string `json:"status"`
		}
		if err := json.Unmarshal(frame.Payload.Event, &data); err != nil {
			return twitch.Event{}, true, errors.New("invalid EventSub held-message notification")
		}
		if data.MessageID == "" || data.BroadcasterLogin == "" {
			return twitch.Event{}, true, errors.New("held-message notification missing identity")
		}
		action := "message_held"
		kind := twitch.ModerationAction
		if frame.Metadata.Subscription == "channel.chat.user_message_update" {
			action = "message_" + data.Status
			if data.Status == "denied" {
				kind = twitch.ModerationMessageDeleted
			}
		}
		return twitch.Event{Kind: twitch.EventModeration, Moderation: twitch.ModerationEvent{Type: kind, Action: action, DeliveryID: frame.Metadata.ID, Channel: data.BroadcasterLogin, RoomID: data.BroadcasterID, TargetUserID: data.UserID, TargetMessageID: data.MessageID, Timestamp: frame.Metadata.At}}, true, nil
	case "channel.moderate":
		return normalizeModeration(frame)

	}
	return twitch.Event{}, false, nil
}
