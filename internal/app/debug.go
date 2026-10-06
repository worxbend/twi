package app

import (
	"context"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/worxbend/twi/internal/twitch"
)

const (
	debugKeyTextLength  = "text_length"
	debugKeyRawTagCount = "raw_tag_count"
	debugKeyChannel     = "channel"
	debugKeyChannelID   = "channel_id"
	debugKeyError       = "error"
	debugKeyHasError    = "has_error"
	debugKeyMessageID   = "message_id"
	debugKeyAuthorLogin = "author_login"
	debugKeyBadgeCount  = "badge_count"
)

func (m shellModel) debugAppStart(source string, channels int) {
	m.debugLogger.Log(context.Background(), "app.start",
		slog.String("source", source),
		slog.Int("channels", channels),
		slog.String("animation_mode", m.animationMode),
		slog.Bool("mouse_enabled", m.mouseEnabled),
	)
}

func (m shellModel) debugChatMessage(event string, msg twitch.ChatMessage) {
	m.debugLogger.Log(context.Background(), event, chatMessageDebugAttrs(msg)...)
}

func (m shellModel) debugConnectionState(event string, state ConnectionState) {
	m.debugLogger.Log(context.Background(), event, connectionStateDebugAttrs(state)...)
}

func (m shellModel) debugSendQueued(send queuedComposerSend) {
	m.debugLogger.Log(context.Background(), "app.send.queued", queuedSendDebugAttrs(send)...)
}

func (m shellModel) debugSendStart(send queuedComposerSend) {
	m.debugLogger.Log(context.Background(), "app.send.start", queuedSendDebugAttrs(send)...)
}

func (m shellModel) debugSendComplete(send queuedComposerSend, result SendResult, err error) {
	attrs := queuedSendDebugAttrs(send)
	attrs = append(attrs, sendResultDebugAttrs(result, err)...)
	m.debugLogger.Log(context.Background(), "app.send.complete", attrs...)
}

func (m shellModel) debugChannelOpened(channel string) {
	m.debugLogger.Log(context.Background(), "app.channel.opened",
		slog.String(debugKeyChannel, channel),
		slog.Int("open_channels", len(m.channels.channelNames())),
	)
}

func (m shellModel) debugChannelClosed(channel string) {
	m.debugLogger.Log(context.Background(), "app.channel.closed",
		slog.String(debugKeyChannel, channel),
		slog.Int("open_channels", len(m.channels.channelNames())),
	)
}

func (m shellModel) debugChannelJoinFailed(channel string, err error) {
	m.debugLogger.Log(context.Background(), "app.channel.join_failed",
		slog.String(debugKeyChannel, channel),
		slog.String(debugKeyError, err.Error()),
	)
}

func (m shellModel) debugChannelPartFailed(channel string, err error) {
	m.debugLogger.Log(context.Background(), "app.channel.part_failed",
		slog.String(debugKeyChannel, channel),
		slog.String(debugKeyError, err.Error()),
	)
}

func (c *LiveChatClient) debugLiveEvent(event string, attrs ...slog.Attr) {
	if c == nil {
		return
	}
	c.debugLogger.Log(context.Background(), event, attrs...)
}

func (c *LiveChatClient) debugTransportEvent(event twitch.Event) {
	c.debugLiveEvent("twitch.event", twitchEventDebugAttrs(event)...)
}

func (c *LiveChatClient) debugLiveSendRequest(req SendRequest) {
	c.debugLiveEvent("live_chat.send.start", sendRequestDebugAttrs(req)...)
}

func (c *LiveChatClient) debugLiveSendComplete(req SendRequest, result SendResult, err error) {
	attrs := sendRequestDebugAttrs(req)
	attrs = append(attrs, sendResultDebugAttrs(result, err)...)
	c.debugLiveEvent("live_chat.send.complete", attrs...)
}

func connectionStateDebugAttrs(state ConnectionState) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("status", string(state.Status)),
		slog.String(debugKeyChannel, state.Channel),
		slog.String("detail", state.Detail),
		slog.Bool(debugKeyHasError, state.Err != nil),
	}
	if state.Err != nil {
		attrs = append(attrs, slog.String(debugKeyError, state.Err.Error()))
	}
	if !state.At.IsZero() {
		attrs = append(attrs, slog.Time("at", state.At))
	}
	return attrs
}

func chatMessageDebugAttrs(msg twitch.ChatMessage) []slog.Attr {
	attrs := []slog.Attr{
		slog.String(debugKeyMessageID, msg.ID),
		slog.String(debugKeyChannel, msg.Channel),
		slog.String(debugKeyChannelID, msg.ChannelID),
		slog.String("type", string(msg.Type)),
		slog.String(debugKeyAuthorLogin, msg.AuthorLogin),
		slog.String("author_id", msg.AuthorID),
		slog.String("display_name", msg.DisplayName),
		slog.Int(debugKeyTextLength, utf8.RuneCountInString(msg.Text)),
		slog.Int("fragment_count", len(msg.Fragments)),
		slog.Int("emote_count", len(msg.Emotes)),
		slog.Int(debugKeyBadgeCount, len(msg.Badges)),
		slog.Bool("has_reply", msg.Reply != nil),
		slog.Int(debugKeyRawTagCount, len(msg.RawTags)),
		slog.Bool("deleted", msg.Deleted),
	}
	if !msg.Timestamp.IsZero() {
		attrs = append(attrs, slog.Time("timestamp", msg.Timestamp))
	}
	return attrs
}

func twitchEventDebugAttrs(event twitch.Event) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("kind", string(event.Kind)),
		slog.Bool(debugKeyHasError, event.Err != nil),
	}
	if event.Err != nil {
		attrs = append(attrs, slog.String(debugKeyError, event.Err.Error()))
	}
	switch event.Kind {
	case twitch.EventMessage:
		attrs = append(attrs, chatMessageDebugAttrs(event.Message)...)
	case twitch.EventNotice:
		attrs = append(attrs,
			slog.String(debugKeyChannel, event.Notice.Channel),
			slog.String("notice_id", event.Notice.ID),
			slog.Int(debugKeyTextLength, utf8.RuneCountInString(event.Notice.Text)),
			slog.Int(debugKeyRawTagCount, len(event.Notice.RawTags)),
		)
	case twitch.EventUserNotice:
		attrs = append(attrs,
			slog.String(debugKeyChannel, event.UserNotice.Channel),
			slog.String(debugKeyChannelID, event.UserNotice.RoomID),
			slog.String("notice_id", event.UserNotice.ID),
			slog.String(debugKeyMessageID, event.UserNotice.MessageID),
			slog.String(debugKeyAuthorLogin, event.UserNotice.AuthorLogin),
			slog.Int(debugKeyTextLength, utf8.RuneCountInString(event.UserNotice.Text)),
			slog.Int("system_text_length", utf8.RuneCountInString(event.UserNotice.SystemText)),
			slog.Int("fragment_count", len(event.UserNotice.Fragments)),
			slog.Int("emote_count", len(event.UserNotice.Emotes)),
			slog.Int(debugKeyBadgeCount, len(event.UserNotice.Badges)),
			slog.Int("param_count", len(event.UserNotice.Params)),
			slog.Int(debugKeyRawTagCount, len(event.UserNotice.RawTags)),
		)
	case twitch.EventRoomState:
		attrs = append(attrs,
			slog.String(debugKeyChannel, event.RoomState.Channel),
			slog.String(debugKeyChannelID, event.RoomState.RoomID),
			slog.Int("state_count", len(event.RoomState.State)),
			slog.Int(debugKeyRawTagCount, len(event.RoomState.RawTags)),
		)
	case twitch.EventModeration:
		attrs = append(attrs,
			slog.String("moderation_type", string(event.Moderation.Type)),
			slog.String(debugKeyChannel, event.Moderation.Channel),
			slog.String(debugKeyChannelID, event.Moderation.RoomID),
			slog.String("target_user_id", event.Moderation.TargetUserID),
			slog.String("target_login", event.Moderation.TargetLogin),
			slog.String("target_message_id", event.Moderation.TargetMessageID),
			slog.Int64("ban_duration_ms", int64(event.Moderation.BanDuration/time.Millisecond)),
			slog.Int(debugKeyTextLength, utf8.RuneCountInString(event.Moderation.Text)),
			slog.Int(debugKeyRawTagCount, len(event.Moderation.RawTags)),
		)
	case twitch.EventUserState:
		attrs = append(attrs,
			slog.String(debugKeyChannel, event.UserState.Channel),
			slog.String(debugKeyAuthorLogin, event.UserState.AuthorLogin),
			slog.String("author_id", event.UserState.AuthorID),
			slog.Int(debugKeyBadgeCount, len(event.UserState.Badges)),
			slog.Int("emote_set_count", len(event.UserState.EmoteSets)),
			slog.Int(debugKeyRawTagCount, len(event.UserState.RawTags)),
		)
	case twitch.EventConnection:
		attrs = append(attrs, twitchConnectionEventDebugAttrs(event.Connection)...)
	case twitch.EventRaw:
		attrs = append(attrs,
			slog.String("raw_type", event.Raw.RawType),
			slog.Int(debugKeyTextLength, utf8.RuneCountInString(event.Raw.Text)),
			slog.Int("raw_length", utf8.RuneCountInString(event.Raw.Raw)),
			slog.Int(debugKeyRawTagCount, len(event.Raw.RawTags)),
			slog.Bool("todo_present", event.Raw.TODO != ""),
		)
	}
	return attrs
}

func twitchConnectionEventDebugAttrs(event twitch.ConnectionEvent) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("connection_type", string(event.Type)),
		slog.String("reason", event.Reason),
		slog.Bool(debugKeyHasError, event.Err != nil),
	}
	if event.Err != nil {
		attrs = append(attrs, slog.String(debugKeyError, event.Err.Error()))
	}
	if !event.At.IsZero() {
		attrs = append(attrs, slog.Time("at", event.At))
	}
	return attrs
}

func sendRequestDebugAttrs(req SendRequest) []slog.Attr {
	return []slog.Attr{
		slog.String(debugKeyChannel, req.Channel),
		slog.Bool("is_reply", req.ReplyToMessageID != ""),
		slog.Bool("is_action", req.Action),
		slog.String("reply_to_message_id", req.ReplyToMessageID),
		slog.Int(debugKeyTextLength, utf8.RuneCountInString(req.Text)),
	}
}

func queuedSendDebugAttrs(send queuedComposerSend) []slog.Attr {
	return []slog.Attr{
		slog.Int("send_id", send.ID),
		slog.String(debugKeyChannel, send.Channel),
		slog.Bool("is_reply", send.ReplyToMessageID != ""),
		slog.Bool("is_action", send.Action),
		slog.String("reply_to_message_id", send.ReplyToMessageID),
		slog.Int(debugKeyTextLength, utf8.RuneCountInString(send.Text)),
		slog.Int("draft_length", utf8.RuneCountInString(send.Draft)),
	}
}

func sendResultDebugAttrs(result SendResult, err error) []slog.Attr {
	attrs := []slog.Attr{
		slog.String(debugKeyMessageID, result.MessageID),
		slog.Bool("accepted", err == nil && !result.RateLimited),
		slog.Bool("rate_limited", result.RateLimited),
		slog.Int64("retry_after_ms", int64(result.RetryAfter/time.Millisecond)),
		slog.String("detail", result.Detail),
		slog.Bool(debugKeyHasError, err != nil),
	}
	if !result.AcceptedAt.IsZero() {
		attrs = append(attrs, slog.Time("accepted_at", result.AcceptedAt))
	}
	if err != nil {
		attrs = append(attrs, slog.String(debugKeyError, err.Error()))
	}
	return attrs
}
