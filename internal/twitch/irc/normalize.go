package irc

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	gempir "github.com/gempir/go-twitch-irc/v4"

	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
)

const rawEventTODO = "TODO: add a typed normalizer for this Twitch IRC message before rendering it in the app"

const twitchEmoteStaticURLTemplate = "https://static-cdn.jtvnw.net/emoticons/v2/%s/static/light/2.0"

// NormalizeMessage converts a go-twitch-irc callback payload into the
// internal event model used by the rest of twi.
func NormalizeMessage(message gempir.Message) twitch.Event {
	switch message := message.(type) {
	case *gempir.PrivateMessage:
		return NormalizePrivateMessage(*message)
	case *gempir.NoticeMessage:
		return NormalizeNoticeMessage(*message)
	case *gempir.UserNoticeMessage:
		return NormalizeUserNoticeMessage(*message)
	case *gempir.RoomStateMessage:
		return NormalizeRoomStateMessage(*message)
	case *gempir.ClearChatMessage:
		return NormalizeClearChatMessage(*message)
	case *gempir.ClearMessage:
		return NormalizeClearMessage(*message)
	case *gempir.UserStateMessage:
		return NormalizeUserStateMessage(*message)
	case *gempir.UserJoinMessage:
		return NormalizeUserJoinMessage(*message, time.Now())
	case *gempir.UserPartMessage:
		return NormalizeUserPartMessage(*message, time.Now())
	case *gempir.ReconnectMessage:
		return NormalizeReconnectMessage(*message, time.Now())
	case *gempir.RawMessage:
		return NormalizeRawMessage(*message)
	default:
		return twitch.Event{
			Kind: twitch.EventRaw,
			Raw:  twitch.RawEvent{TODO: rawEventTODO},
		}
	}
}

func NormalizePrivateMessage(message gempir.PrivateMessage) twitch.Event {
	chat := normalizeChatMessage(
		message.ID,
		message.Channel,
		message.RoomID,
		message.Time,
		message.User,
		message.Message,
		message.Emotes,
		message.Reply,
		message.Action,
		message.Tags,
	)
	return twitch.Event{Kind: twitch.EventMessage, Message: chat}
}

func NormalizeNoticeMessage(message gempir.NoticeMessage) twitch.Event {
	return twitch.Event{
		Kind: twitch.EventNotice,
		Notice: twitch.Notice{
			Channel:    message.Channel,
			ID:         message.MsgID,
			Text:       textsafe.Display(message.Message),
			AuthFailed: isAuthNoticeText(message.Message),
			RawTags:    cloneStringMap(message.Tags),
		},
	}
}

// authNoticeTexts are the NOTICE bodies Twitch sends when login itself fails.
//
// These arrive before registration completes, on the "*" channel, with no
// msg-id, so the text is the only signal available. Every tagged notice --
// msg_banned, no_permission, msg_channel_suspended and the rest -- describes
// channel or account state on a connection that authenticated fine, and none
// of them belong here.
var authNoticeTexts = []string{
	"login authentication failed",
	"login unsuccessful",
	"improperly formatted auth",
}

// isAuthNoticeText reports whether a NOTICE body means the credentials were
// rejected.
//
// It matches the specific bodies Twitch sends for a failed login rather than
// searching for "auth", "invalid", "permission" or "scope" anywhere in the
// notice. That broad matching flipped the whole client to a failed state --
// red status bar, "verify your OAuth token" -- on an ordinary no_permission
// notice, while the connection was perfectly healthy.
func isAuthNoticeText(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	return slices.ContainsFunc(authNoticeTexts, func(known string) bool {
		return strings.Contains(text, known)
	})
}

func NormalizeUserNoticeMessage(message gempir.UserNoticeMessage) twitch.Event {
	emotes := normalizeEmotes(message.Emotes)
	text := textsafe.Display(message.Message)
	return twitch.Event{
		Kind: twitch.EventUserNotice,
		UserNotice: twitch.UserNotice{
			ID:          message.ID,
			Channel:     message.Channel,
			RoomID:      message.RoomID,
			Timestamp:   message.Time,
			AuthorLogin: userLogin(message.User, message.Tags),
			AuthorID:    message.User.ID,
			DisplayName: textsafe.Display(message.User.DisplayName),
			AuthorColor: message.User.Color,
			Badges:      normalizeBadges(message.User.Badges, message.Tags),
			Text:        text,
			SystemText:  textsafe.Display(message.SystemMsg),
			MessageID:   message.MsgID,
			Params:      cloneStringMap(message.MsgParams),
			Emotes:      emotes,
			Fragments:   normalizeMessageFragments(text, emotes),
			RawTags:     cloneStringMap(message.Tags),
		},
	}
}

func NormalizeRoomStateMessage(message gempir.RoomStateMessage) twitch.Event {
	return twitch.Event{
		Kind: twitch.EventRoomState,
		RoomState: twitch.RoomState{
			Channel: message.Channel,
			RoomID:  message.RoomID,
			State:   cloneIntMap(message.State),
			RawTags: cloneStringMap(message.Tags),
		},
	}
}

func NormalizeClearChatMessage(message gempir.ClearChatMessage) twitch.Event {
	eventType := twitch.ModerationChatCleared
	if message.TargetUsername != "" {
		eventType = twitch.ModerationUserBanned
	}
	if message.BanDuration > 0 {
		eventType = twitch.ModerationUserTimedOut
	}

	return twitch.Event{
		Kind: twitch.EventModeration,
		Moderation: twitch.ModerationEvent{
			Type:         eventType,
			Channel:      message.Channel,
			RoomID:       message.RoomID,
			Timestamp:    message.Time,
			TargetUserID: message.TargetUserID,
			TargetLogin:  textsafe.Display(message.TargetUsername),
			BanDuration:  time.Duration(message.BanDuration) * time.Second,
			Text:         textsafe.Display(message.Message),
			RawTags:      cloneStringMap(message.Tags),
		},
	}
}

func NormalizeClearMessage(message gempir.ClearMessage) twitch.Event {
	return twitch.Event{
		Kind: twitch.EventModeration,
		Moderation: twitch.ModerationEvent{
			Type:            twitch.ModerationMessageDeleted,
			Channel:         message.Channel,
			TargetLogin:     textsafe.Display(message.Login),
			TargetMessageID: message.TargetMsgID,
			Text:            textsafe.Display(message.Message),
			RawTags:         cloneStringMap(message.Tags),
		},
	}
}

func NormalizeUserStateMessage(message gempir.UserStateMessage) twitch.Event {
	return twitch.Event{
		Kind: twitch.EventUserState,
		UserState: twitch.UserState{
			Channel:     message.Channel,
			AuthorLogin: textsafe.Display(message.User.Name),
			AuthorID:    message.User.ID,
			DisplayName: textsafe.Display(message.User.DisplayName),
			AuthorColor: message.User.Color,
			Badges:      normalizeBadges(message.User.Badges, message.Tags),
			EmoteSets:   cloneStringSlice(message.EmoteSets),
			RawTags:     cloneStringMap(message.Tags),
		},
	}
}

// NormalizeUserJoinMessage converts a membership JOIN into a normalized
// event. JOIN/PART carry no IRC tags, so at is supplied by the caller rather
// than read from the message.
func NormalizeUserJoinMessage(message gempir.UserJoinMessage, at time.Time) twitch.Event {
	return membershipEvent(twitch.MembershipJoin, message.Channel, message.User, at)
}

// NormalizeUserPartMessage converts a membership PART into a normalized
// event.
func NormalizeUserPartMessage(message gempir.UserPartMessage, at time.Time) twitch.Event {
	return membershipEvent(twitch.MembershipPart, message.Channel, message.User, at)
}

func membershipEvent(membership twitch.MembershipType, channel, login string, at time.Time) twitch.Event {
	return twitch.Event{
		Kind: twitch.EventMembership,
		Membership: twitch.MembershipEvent{
			Type:    membership,
			Channel: strings.TrimPrefix(strings.TrimSpace(channel), "#"),
			Login:   strings.ToLower(strings.TrimSpace(textsafe.Display(login))),
			At:      at,
		},
	}
}

func NormalizeReconnectMessage(message gempir.ReconnectMessage, at time.Time) twitch.Event {
	return twitch.Event{
		Kind: twitch.EventConnection,
		Connection: twitch.ConnectionEvent{
			Type:   twitch.ConnectionEventReconnect,
			At:     at,
			Reason: strings.TrimSpace(message.Raw),
		},
	}
}

func NormalizeConnect(at time.Time) twitch.Event {
	return twitch.Event{
		Kind: twitch.EventConnection,
		Connection: twitch.ConnectionEvent{
			Type: twitch.ConnectionEventConnect,
			At:   at,
		},
	}
}

func NormalizeDisconnect(err error, at time.Time) twitch.Event {
	event := twitch.Event{
		Kind: twitch.EventConnection,
		Connection: twitch.ConnectionEvent{
			Type: twitch.ConnectionEventDisconnect,
			At:   at,
			Err:  err,
		},
	}
	if err != nil {
		event.Err = err
	}
	return event
}

func NormalizeRawMessage(message gempir.RawMessage) twitch.Event {
	return twitch.Event{
		Kind: twitch.EventRaw,
		Raw: twitch.RawEvent{
			RawType: message.RawType,
			Text:    textsafe.Display(message.Message),
			Raw:     message.Raw,
			RawTags: cloneStringMap(message.Tags),
			TODO:    rawEventTODO,
		},
	}
}

// normalizeChatMessage builds the internal message from a PRIVMSG.
//
// Everything that will be drawn goes through textsafe.Display on the way in.
// A chat message is attacker-controlled text that lands in every viewer's
// terminal with no interaction at all, so an ESC sequence pasted into chat
// would otherwise be executed rather than shown. Sanitizing here, before the
// fragments are cut, keeps the split points aligned with what is rendered.
func normalizeChatMessage(id, channel, channelID string, timestamp time.Time, user gempir.User, text string, ircEmotes []*gempir.Emote, reply *gempir.Reply, action bool, tags map[string]string) twitch.ChatMessage {
	emotes := normalizeEmotes(ircEmotes)
	text = textsafe.Display(text)
	messageType := twitch.MessageTypeChat
	if action {
		messageType = twitch.MessageTypeAction
	}

	return twitch.ChatMessage{
		ID:           id,
		Channel:      channel,
		ChannelID:    channelID,
		Timestamp:    timestamp,
		AuthorLogin:  textsafe.Display(user.Name),
		AuthorID:     user.ID,
		DisplayName:  textsafe.Display(user.DisplayName),
		AuthorColor:  user.Color,
		Badges:       normalizeBadges(user.Badges, tags),
		Text:         text,
		Fragments:    normalizeMessageFragments(text, emotes),
		Emotes:       emotes,
		Reply:        normalizeReply(reply),
		Type:         messageType,
		Bits:         parseBitsTag(tags),
		FirstMessage: parseFirstMessageTag(tags),
		RawTags:      cloneStringMap(tags),
	}
}

// parseFirstMessageTag reads Twitch's "first-msg" tag, set to 1 on a
// chatter's first ever message in the channel. Anything other than "1" -- a
// missing tag, "0", or a value Twitch changes later -- means "not first",
// which is the safe default: over-highlighting regulars is worse than missing
// the occasional newcomer.
func parseFirstMessageTag(tags map[string]string) bool {
	return strings.TrimSpace(tags["first-msg"]) == "1"
}

// parseBitsTag reads a PRIVMSG's "bits" tag, present only on cheer messages.
// A missing or malformed tag is treated as 0 bits, not an error - Twitch's
// own clients handle it the same way.
func parseBitsTag(tags map[string]string) int {
	raw := strings.TrimSpace(tags["bits"])
	if raw == "" {
		return 0
	}
	bits, err := strconv.Atoi(raw)
	if err != nil || bits < 0 {
		return 0
	}
	return bits
}

func normalizeBadges(in map[string]int, tags map[string]string) []twitch.Badge {
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

	badges := make([]twitch.Badge, 0, len(keys))
	for _, key := range keys {
		badges = append(badges, twitch.Badge{
			SetID: textsafe.Display(key),
			ID:    strconv.Itoa(in[key]),
			Info:  textsafe.Display(info[key]),
		})
	}
	return badges
}

func userLogin(user gempir.User, tags map[string]string) string {
	if tags["login"] != "" {
		return textsafe.Display(tags["login"])
	}
	return textsafe.Display(user.Name)
}

func parseBadges(raw string, info map[string]string) []twitch.Badge {
	if raw == "" {
		return nil
	}

	var badges []twitch.Badge
	for _, rawBadge := range strings.Split(raw, ",") {
		setID, id, ok := strings.Cut(rawBadge, "/")
		if !ok || setID == "" {
			continue
		}
		badges = append(badges, twitch.Badge{
			SetID: textsafe.Display(setID),
			ID:    textsafe.Display(id),
			Info:  textsafe.Display(info[setID]),
		})
	}
	return badges
}

func normalizeEmotes(in []*gempir.Emote) []twitch.Emote {
	var out []twitch.Emote
	for _, emote := range in {
		if emote == nil {
			continue
		}
		for _, position := range emote.Positions {
			out = append(out, twitch.Emote{
				ID:    emote.ID,
				Name:  textsafe.Display(emote.Name),
				Start: position.Start,
				End:   position.End,
				Ref:   twitchEmoteAssetRef(emote.ID),
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

func normalizeReply(reply *gempir.Reply) *twitch.Reply {
	if reply == nil {
		return nil
	}
	return &twitch.Reply{
		ParentMessageID: reply.ParentMsgID,
		ParentAuthorID:  reply.ParentUserID,
		ParentLogin:     textsafe.Display(reply.ParentUserLogin),
		ParentAuthor:    textsafe.Display(reply.ParentDisplayName),
		ParentText:      textsafe.Display(reply.ParentMsgBody),
	}
}

func normalizeMessageFragments(text string, emotes []twitch.Emote) []twitch.MessageFragment {
	if text == "" {
		return nil
	}
	if len(emotes) == 0 {
		return splitMentionFragments(text)
	}

	runes := []rune(text)
	var fragments []twitch.MessageFragment
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
		fragments = append(fragments, twitch.MessageFragment{
			Type: twitch.FragmentEmote,
			Text: emoteText,
			Ref:  twitchEmoteAssetRef(emote.ID),
		})
		next = emote.End + 1
	}
	if next < len(runes) {
		fragments = append(fragments, splitMentionFragments(string(runes[next:]))...)
	}
	return coalesceMessageFragments(fragments)
}

func twitchEmoteAssetRef(id string) twitch.AssetRef {
	id = strings.TrimSpace(id)
	ref := twitch.AssetRef{Kind: "twitch_emote", ID: id}
	if id != "" {
		ref.URL = fmt.Sprintf(twitchEmoteStaticURLTemplate, id)
	}
	return ref
}

func splitMentionFragments(text string) []twitch.MessageFragment {
	if text == "" {
		return nil
	}

	runes := []rune(text)
	var fragments []twitch.MessageFragment
	start := 0
	for i := 0; i < len(runes); {
		if runes[i] != '@' || i+1 >= len(runes) || !isMentionRune(runes[i+1]) {
			i++
			continue
		}
		if i > start {
			fragments = append(fragments, twitch.MessageFragment{Type: twitch.FragmentText, Text: string(runes[start:i])})
		}
		end := i + 2
		for end < len(runes) && isMentionRune(runes[end]) {
			end++
		}
		fragments = append(fragments, twitch.MessageFragment{Type: twitch.FragmentMention, Text: string(runes[i:end])})
		i = end
		start = end
	}
	if start < len(runes) {
		fragments = append(fragments, twitch.MessageFragment{Type: twitch.FragmentText, Text: string(runes[start:])})
	}
	return fragments
}

func isMentionRune(r rune) bool {
	return twitch.IsLoginRune(r)
}

func coalesceMessageFragments(in []twitch.MessageFragment) []twitch.MessageFragment {
	if len(in) < 2 {
		return in
	}

	out := make([]twitch.MessageFragment, 0, len(in))
	for _, fragment := range in {
		if fragment.Text == "" && fragment.Ref.ID == "" {
			continue
		}
		last := len(out) - 1
		if last >= 0 && out[last].Type == twitch.FragmentText && fragment.Type == twitch.FragmentText && out[last].Ref == (twitch.AssetRef{}) && fragment.Ref == (twitch.AssetRef{}) {
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
