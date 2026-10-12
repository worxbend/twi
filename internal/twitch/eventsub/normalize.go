package eventsub

import (
	"encoding/json"
	"errors"
	"github.com/worxbend/twi/internal/twitch"
)

func normalize(frame envelope) (twitch.Event, error) {
	if event, handled, err := normalizeControl(frame); handled {
		return event, err
	}
	var data struct {
		TargetUserID     string      `json:"target_user_id"`
		BroadcasterID    string      `json:"broadcaster_user_id"`
		BroadcasterLogin string      `json:"broadcaster_user_login"`
		ChatterID        string      `json:"chatter_user_id"`
		ChatterLogin     string      `json:"chatter_user_login"`
		ChatterName      string      `json:"chatter_user_name"`
		ID               string      `json:"message_id"`
		Color            string      `json:"color"`
		SourceID         string      `json:"source_broadcaster_user_id"`
		SourceLogin      string      `json:"source_broadcaster_user_login"`
		SourceMessageID  string      `json:"source_message_id"`
		Message          chatBody    `json:"message"`
		MessageType      string      `json:"message_type"`
		Badges           []chatBadge `json:"badges"`
		SourceBadges     []chatBadge `json:"source_badges"`
		Cheer            *struct {
			Bits int `json:"bits"`
		} `json:"cheer"`
		Reply *struct {
			ID     string `json:"parent_message_id"`
			UserID string `json:"parent_user_id"`
			Login  string `json:"parent_user_login"`
			Name   string `json:"parent_user_name"`
			Body   string `json:"parent_message_body"`
		} `json:"reply"`
	}
	if err := json.Unmarshal(frame.Payload.Event, &data); err != nil {
		return twitch.Event{}, errors.New("invalid EventSub chat notification")
	}
	switch frame.Metadata.Subscription {
	case "channel.chat.message_delete", "channel.chat.clear", "channel.chat.clear_user_messages":
		if data.BroadcasterLogin == "" {
			return twitch.Event{}, errors.New("EventSub moderation control missing channel")
		}
		if frame.Metadata.Subscription == "channel.chat.message_delete" && data.ID == "" {
			return twitch.Event{}, errors.New("EventSub message deletion missing target message")
		}
		if frame.Metadata.Subscription == "channel.chat.clear_user_messages" && data.TargetUserID == "" {
			return twitch.Event{}, errors.New("EventSub user clear missing target user")
		}

		kind := twitch.ModerationChatCleared
		if frame.Metadata.Subscription == "channel.chat.message_delete" {
			kind = twitch.ModerationMessageDeleted
		}
		if frame.Metadata.Subscription == "channel.chat.clear_user_messages" {
			kind = twitch.ModerationUserMessagesCleared
		}
		return twitch.Event{Kind: twitch.EventModeration, Moderation: twitch.ModerationEvent{DeliveryID: frame.Metadata.ID, Type: kind, Channel: data.BroadcasterLogin, RoomID: data.BroadcasterID, Timestamp: frame.Metadata.At, TargetMessageID: data.ID, TargetUserID: data.TargetUserID}}, nil
	}
	if frame.Metadata.Subscription != "channel.chat.message" {
		return twitch.Event{Kind: twitch.EventRaw}, nil
	}
	if data.ID == "" || data.BroadcasterLogin == "" {
		return twitch.Event{}, errors.New("EventSub chat notification missing identity")
	}
	message := twitch.ChatMessage{ID: data.ID, Channel: data.BroadcasterLogin, ChannelID: data.BroadcasterID, Timestamp: frame.Metadata.At, AuthorID: data.ChatterID, AuthorLogin: data.ChatterLogin, DisplayName: data.ChatterName, AuthorColor: data.Color, Text: data.Message.Text, Type: twitch.MessageTypeChat, DeliveryID: frame.Metadata.ID, SourceChannelID: data.SourceID, SourceChannelLogin: data.SourceLogin, SourceMessageID: data.SourceMessageID}
	if data.Reply != nil {
		message.Reply = &twitch.Reply{ParentMessageID: data.Reply.ID, ParentAuthorID: data.Reply.UserID, ParentLogin: data.Reply.Login, ParentAuthor: data.Reply.Name, ParentText: data.Reply.Body}
	}
	message.Fragments = normalizeFragments(data.Message.Fragments)
	message.Badges = normalizeBadges(data.Badges)
	message.SourceBadges = normalizeBadges(data.SourceBadges)
	message.PlatformMessageType = data.MessageType
	message.FirstMessage = data.MessageType == "user_intro"
	if data.Cheer != nil {
		message.Bits = max(0, data.Cheer.Bits)
	}
	if text, action := normalizeAction(message.Text); action {
		message.Text = text
		message.Type = twitch.MessageTypeAction
		stripActionFragmentBoundaries(message.Fragments)
	}
	return twitch.Event{Kind: twitch.EventMessage, Message: message}, nil
}
