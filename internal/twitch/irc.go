package twitch

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	irc "github.com/gempir/go-twitch-irc/v4"
)

const rawEventTODO = "TODO: add a typed normalizer for this Twitch IRC message before rendering it in the app"

// NormalizeIRCMessage converts a go-twitch-irc callback payload into the
// internal event model used by the rest of twi.
func NormalizeIRCMessage(message irc.Message) Event {
	switch message := message.(type) {
	case *irc.PrivateMessage:
		return NormalizeIRCPrivateMessage(*message)
	case *irc.NoticeMessage:
		return NormalizeIRCNoticeMessage(*message)
	case *irc.UserNoticeMessage:
		return NormalizeIRCUserNoticeMessage(*message)
	case *irc.RoomStateMessage:
		return NormalizeIRCRoomStateMessage(*message)
	case *irc.ClearChatMessage:
		return NormalizeIRCClearChatMessage(*message)
	case *irc.ClearMessage:
		return NormalizeIRCClearMessage(*message)
	case *irc.UserStateMessage:
		return NormalizeIRCUserStateMessage(*message)
	case *irc.ReconnectMessage:
		return NormalizeIRCReconnectMessage(*message, time.Now())
	case *irc.RawMessage:
		return NormalizeIRCRawMessage(*message)
	default:
		return Event{
			Kind: EventRaw,
			Raw:  RawEvent{TODO: rawEventTODO},
		}
	}
}

func NormalizeIRCPrivateMessage(message irc.PrivateMessage) Event {
	chat := normalizeIRCChatMessage(
		message.ID,
		message.Channel,
		message.Time,
		message.User,
		message.Message,
		message.Emotes,
		message.Reply,
		message.Action,
		message.Tags,
	)
	return Event{Kind: EventMessage, Message: chat}
}

func NormalizeIRCNoticeMessage(message irc.NoticeMessage) Event {
	return Event{
		Kind: EventNotice,
		Notice: Notice{
			Channel: message.Channel,
			ID:      message.MsgID,
			Text:    message.Message,
			RawTags: cloneStringMap(message.Tags),
		},
	}
}

func NormalizeIRCUserNoticeMessage(message irc.UserNoticeMessage) Event {
	emotes := normalizeIRCEmotes(message.Emotes)
	return Event{
		Kind: EventUserNotice,
		UserNotice: UserNotice{
			ID:          message.ID,
			Channel:     message.Channel,
			RoomID:      message.RoomID,
			Timestamp:   message.Time,
			AuthorLogin: userLogin(message.User, message.Tags),
			AuthorID:    message.User.ID,
			DisplayName: message.User.DisplayName,
			AuthorColor: message.User.Color,
			Badges:      normalizeIRCBadges(message.User.Badges, message.Tags),
			Text:        message.Message,
			SystemText:  message.SystemMsg,
			MessageID:   message.MsgID,
			Params:      cloneStringMap(message.MsgParams),
			Emotes:      emotes,
			Fragments:   normalizeMessageFragments(message.Message, emotes),
			RawTags:     cloneStringMap(message.Tags),
		},
	}
}

func NormalizeIRCRoomStateMessage(message irc.RoomStateMessage) Event {
	return Event{
		Kind: EventRoomState,
		RoomState: RoomState{
			Channel: message.Channel,
			RoomID:  message.RoomID,
			State:   cloneIntMap(message.State),
			RawTags: cloneStringMap(message.Tags),
		},
	}
}

func NormalizeIRCClearChatMessage(message irc.ClearChatMessage) Event {
	eventType := ModerationChatCleared
	if message.TargetUsername != "" {
		eventType = ModerationUserBanned
	}
	if message.BanDuration > 0 {
		eventType = ModerationUserTimedOut
	}

	return Event{
		Kind: EventModeration,
		Moderation: ModerationEvent{
			Type:         eventType,
			Channel:      message.Channel,
			RoomID:       message.RoomID,
			Timestamp:    message.Time,
			TargetUserID: message.TargetUserID,
			TargetLogin:  message.TargetUsername,
			BanDuration:  time.Duration(message.BanDuration) * time.Second,
			Text:         message.Message,
			RawTags:      cloneStringMap(message.Tags),
		},
	}
}

func NormalizeIRCClearMessage(message irc.ClearMessage) Event {
	return Event{
		Kind: EventModeration,
		Moderation: ModerationEvent{
			Type:            ModerationMessageDeleted,
			Channel:         message.Channel,
			TargetLogin:     message.Login,
			TargetMessageID: message.TargetMsgID,
			Text:            message.Message,
			RawTags:         cloneStringMap(message.Tags),
		},
	}
}

func NormalizeIRCUserStateMessage(message irc.UserStateMessage) Event {
	return Event{
		Kind: EventUserState,
		UserState: UserState{
			Channel:     message.Channel,
			AuthorLogin: message.User.Name,
			AuthorID:    message.User.ID,
			DisplayName: message.User.DisplayName,
			AuthorColor: message.User.Color,
			Badges:      normalizeIRCBadges(message.User.Badges, message.Tags),
			EmoteSets:   cloneStringSlice(message.EmoteSets),
			RawTags:     cloneStringMap(message.Tags),
		},
	}
}

func NormalizeIRCReconnectMessage(message irc.ReconnectMessage, at time.Time) Event {
	return Event{
		Kind: EventConnection,
		Connection: ConnectionEvent{
			Type:   ConnectionEventReconnect,
			At:     at,
			Reason: strings.TrimSpace(message.Raw),
		},
	}
}

func NormalizeIRCConnect(at time.Time) Event {
	return Event{
		Kind: EventConnection,
		Connection: ConnectionEvent{
			Type: ConnectionEventConnect,
			At:   at,
		},
	}
}

func NormalizeIRCDisconnect(err error, at time.Time) Event {
	event := Event{
		Kind: EventConnection,
		Connection: ConnectionEvent{
			Type: ConnectionEventDisconnect,
			At:   at,
			Err:  err,
		},
	}
	if err != nil {
		event.Err = err
	}
	return event
}

func NormalizeIRCRawMessage(message irc.RawMessage) Event {
	return Event{
		Kind: EventRaw,
		Raw: RawEvent{
			RawType: message.RawType,
			Text:    message.Message,
			Raw:     message.Raw,
			RawTags: cloneStringMap(message.Tags),
			TODO:    rawEventTODO,
		},
	}
}

func normalizeIRCChatMessage(id, channel string, timestamp time.Time, user irc.User, text string, ircEmotes []*irc.Emote, reply *irc.Reply, action bool, tags map[string]string) ChatMessage {
	emotes := normalizeIRCEmotes(ircEmotes)
	messageType := MessageTypeChat
	if action {
		messageType = MessageTypeAction
	}

	return ChatMessage{
		ID:          id,
		Channel:     channel,
		Timestamp:   timestamp,
		AuthorLogin: user.Name,
		AuthorID:    user.ID,
		DisplayName: user.DisplayName,
		AuthorColor: user.Color,
		Badges:      normalizeIRCBadges(user.Badges, tags),
		Text:        text,
		Fragments:   normalizeMessageFragments(text, emotes),
		Emotes:      emotes,
		Reply:       normalizeIRCReply(reply),
		Type:        messageType,
		RawTags:     cloneStringMap(tags),
	}
}

func normalizeIRCBadges(in map[string]int, tags map[string]string) []Badge {
	info := parseBadgeInfo(tags["badge-info"])
	if rawBadges := tags["badges"]; rawBadges != "" {
		return parseBadges(rawBadges, info)
	}
	if len(in) == 0 {
		return nil
	}

	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	badges := make([]Badge, 0, len(keys))
	for _, key := range keys {
		badges = append(badges, Badge{
			SetID: key,
			ID:    strconv.Itoa(in[key]),
			Info:  info[key],
		})
	}
	return badges
}

func userLogin(user irc.User, tags map[string]string) string {
	if tags["login"] != "" {
		return tags["login"]
	}
	return user.Name
}

func parseBadges(raw string, info map[string]string) []Badge {
	if raw == "" {
		return nil
	}

	var badges []Badge
	for _, rawBadge := range strings.Split(raw, ",") {
		setID, id, ok := strings.Cut(rawBadge, "/")
		if !ok || setID == "" {
			continue
		}
		badges = append(badges, Badge{
			SetID: setID,
			ID:    id,
			Info:  info[setID],
		})
	}
	return badges
}

func normalizeIRCEmotes(in []*irc.Emote) []Emote {
	var out []Emote
	for _, emote := range in {
		if emote == nil {
			continue
		}
		for _, position := range emote.Positions {
			out = append(out, Emote{
				ID:    emote.ID,
				Name:  emote.Name,
				Start: position.Start,
				End:   position.End,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start == out[j].Start {
			return out[i].End < out[j].End
		}
		return out[i].Start < out[j].Start
	})
	return out
}

func normalizeIRCReply(reply *irc.Reply) *Reply {
	if reply == nil {
		return nil
	}
	return &Reply{
		ParentMessageID: reply.ParentMsgID,
		ParentAuthorID:  reply.ParentUserID,
		ParentLogin:     reply.ParentUserLogin,
		ParentAuthor:    reply.ParentDisplayName,
		ParentText:      reply.ParentMsgBody,
	}
}

func normalizeMessageFragments(text string, emotes []Emote) []MessageFragment {
	if text == "" {
		return nil
	}
	if len(emotes) == 0 {
		return splitMentionFragments(text)
	}

	runes := []rune(text)
	var fragments []MessageFragment
	next := 0
	for _, emote := range emotes {
		if emote.Start < next || emote.Start < 0 || emote.End < emote.Start || emote.End >= len(runes) {
			continue
		}
		if emote.Start > next {
			fragments = append(fragments, splitMentionFragments(string(runes[next:emote.Start]))...)
		}

		emoteText := emote.Name
		if emoteText == "" {
			emoteText = string(runes[emote.Start : emote.End+1])
		}
		fragments = append(fragments, MessageFragment{
			Type: FragmentEmote,
			Text: emoteText,
			Ref:  AssetRef{Kind: "twitch_emote", ID: emote.ID},
		})
		next = emote.End + 1
	}
	if next < len(runes) {
		fragments = append(fragments, splitMentionFragments(string(runes[next:]))...)
	}
	return coalesceMessageFragments(fragments)
}

func splitMentionFragments(text string) []MessageFragment {
	if text == "" {
		return nil
	}

	runes := []rune(text)
	var fragments []MessageFragment
	start := 0
	for i := 0; i < len(runes); {
		if runes[i] != '@' || i+1 >= len(runes) || !isMentionRune(runes[i+1]) {
			i++
			continue
		}
		if i > start {
			fragments = append(fragments, MessageFragment{Type: FragmentText, Text: string(runes[start:i])})
		}
		end := i + 2
		for end < len(runes) && isMentionRune(runes[end]) {
			end++
		}
		fragments = append(fragments, MessageFragment{Type: FragmentMention, Text: string(runes[i:end])})
		i = end
		start = end
	}
	if start < len(runes) {
		fragments = append(fragments, MessageFragment{Type: FragmentText, Text: string(runes[start:])})
	}
	return fragments
}

func isMentionRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func coalesceMessageFragments(in []MessageFragment) []MessageFragment {
	if len(in) < 2 {
		return in
	}

	out := make([]MessageFragment, 0, len(in))
	for _, fragment := range in {
		if fragment.Text == "" && fragment.Ref.ID == "" {
			continue
		}
		last := len(out) - 1
		if last >= 0 && out[last].Type == FragmentText && fragment.Type == FragmentText && out[last].Ref == (AssetRef{}) && fragment.Ref == (AssetRef{}) {
			out[last].Text += fragment.Text
			continue
		}
		out = append(out, fragment)
	}
	return out
}

func parseBadgeInfo(raw string) map[string]string {
	info := make(map[string]string)
	if raw == "" {
		return info
	}
	for _, badge := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(badge, "/")
		if !ok || key == "" {
			continue
		}
		info[key] = value
	}
	return info
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
