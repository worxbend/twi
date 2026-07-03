package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/twitch"
)

func (m mockShellModel) debugAppStart(source string, channels int) {
	m.debugLogger.Log(context.Background(), "app.start",
		slog.String("source", source),
		slog.Int("channels", channels),
		slog.String("animation_mode", m.animationMode),
		slog.String("image_mode", m.imageMode),
		slog.Bool("mouse_enabled", m.mouseEnabled),
	)
}

func (m mockShellModel) debugChatMessage(event string, msg twitch.ChatMessage) {
	m.debugLogger.Log(context.Background(), event, chatMessageDebugAttrs(msg)...)
}

func (m mockShellModel) debugConnectionState(event string, state ConnectionState) {
	m.debugLogger.Log(context.Background(), event, connectionStateDebugAttrs(state)...)
}

func (m mockShellModel) debugAvatarLookupStart(count int) {
	m.debugLogger.Log(context.Background(), "app.avatar_lookup.start", slog.Int("request_count", count))
}

func (m mockShellModel) debugAvatarLookupComplete(results []assets.AvatarResult, err error) {
	found := 0
	for _, result := range results {
		if result.Found {
			found++
		}
	}
	attrs := []slog.Attr{
		slog.Int("result_count", len(results)),
		slog.Int("found_count", found),
		slog.Bool("has_error", err != nil),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	m.debugLogger.Log(context.Background(), "app.avatar_lookup.complete", attrs...)
}

func (m mockShellModel) debugAssetBatchStart(count int) {
	m.debugLogger.Log(context.Background(), "app.asset_batch.start", slog.Int("request_count", count))
}

func (m mockShellModel) debugAssetBatchComplete(results []assetPreparedMsg) {
	attrs := assetBatchDebugAttrs(results)
	m.debugLogger.Log(context.Background(), "app.asset_batch.complete", attrs...)
}

func (m mockShellModel) debugSendQueued(send queuedComposerSend) {
	m.debugLogger.Log(context.Background(), "app.send.queued", queuedSendDebugAttrs(send)...)
}

func (m mockShellModel) debugSendStart(send queuedComposerSend) {
	m.debugLogger.Log(context.Background(), "app.send.start", queuedSendDebugAttrs(send)...)
}

func (m mockShellModel) debugSendComplete(send queuedComposerSend, result SendResult, err error) {
	attrs := queuedSendDebugAttrs(send)
	attrs = append(attrs, sendResultDebugAttrs(result, err)...)
	m.debugLogger.Log(context.Background(), "app.send.complete", attrs...)
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
		slog.String("channel", state.Channel),
		slog.String("detail", state.Detail),
		slog.Bool("has_error", state.Err != nil),
	}
	if state.Err != nil {
		attrs = append(attrs, slog.String("error", state.Err.Error()))
	}
	if !state.At.IsZero() {
		attrs = append(attrs, slog.Time("at", state.At))
	}
	return attrs
}

func chatMessageDebugAttrs(msg twitch.ChatMessage) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("message_id", msg.ID),
		slog.String("channel", msg.Channel),
		slog.String("channel_id", msg.ChannelID),
		slog.String("type", string(msg.Type)),
		slog.String("author_login", msg.AuthorLogin),
		slog.String("author_id", msg.AuthorID),
		slog.String("display_name", msg.DisplayName),
		slog.Int("text_length", len([]rune(msg.Text))),
		slog.Int("fragment_count", len(msg.Fragments)),
		slog.Int("emote_count", len(msg.Emotes)),
		slog.Int("badge_count", len(msg.Badges)),
		slog.Bool("has_reply", msg.Reply != nil),
		slog.Int("raw_tag_count", len(msg.RawTags)),
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
		slog.Bool("has_error", event.Err != nil),
	}
	if event.Err != nil {
		attrs = append(attrs, slog.String("error", event.Err.Error()))
	}
	switch event.Kind {
	case twitch.EventMessage:
		attrs = append(attrs, chatMessageDebugAttrs(event.Message)...)
	case twitch.EventNotice:
		attrs = append(attrs,
			slog.String("channel", event.Notice.Channel),
			slog.String("notice_id", event.Notice.ID),
			slog.Int("text_length", len([]rune(event.Notice.Text))),
			slog.Int("raw_tag_count", len(event.Notice.RawTags)),
		)
	case twitch.EventUserNotice:
		attrs = append(attrs,
			slog.String("channel", event.UserNotice.Channel),
			slog.String("channel_id", event.UserNotice.RoomID),
			slog.String("notice_id", event.UserNotice.ID),
			slog.String("message_id", event.UserNotice.MessageID),
			slog.String("author_login", event.UserNotice.AuthorLogin),
			slog.Int("text_length", len([]rune(event.UserNotice.Text))),
			slog.Int("system_text_length", len([]rune(event.UserNotice.SystemText))),
			slog.Int("fragment_count", len(event.UserNotice.Fragments)),
			slog.Int("emote_count", len(event.UserNotice.Emotes)),
			slog.Int("badge_count", len(event.UserNotice.Badges)),
			slog.Int("param_count", len(event.UserNotice.Params)),
			slog.Int("raw_tag_count", len(event.UserNotice.RawTags)),
		)
	case twitch.EventRoomState:
		attrs = append(attrs,
			slog.String("channel", event.RoomState.Channel),
			slog.String("channel_id", event.RoomState.RoomID),
			slog.Int("state_count", len(event.RoomState.State)),
			slog.Int("raw_tag_count", len(event.RoomState.RawTags)),
		)
	case twitch.EventModeration:
		attrs = append(attrs,
			slog.String("moderation_type", string(event.Moderation.Type)),
			slog.String("channel", event.Moderation.Channel),
			slog.String("channel_id", event.Moderation.RoomID),
			slog.String("target_user_id", event.Moderation.TargetUserID),
			slog.String("target_login", event.Moderation.TargetLogin),
			slog.String("target_message_id", event.Moderation.TargetMessageID),
			slog.Int64("ban_duration_ms", int64(event.Moderation.BanDuration/time.Millisecond)),
			slog.Int("text_length", len([]rune(event.Moderation.Text))),
			slog.Int("raw_tag_count", len(event.Moderation.RawTags)),
		)
	case twitch.EventUserState:
		attrs = append(attrs,
			slog.String("channel", event.UserState.Channel),
			slog.String("author_login", event.UserState.AuthorLogin),
			slog.String("author_id", event.UserState.AuthorID),
			slog.Int("badge_count", len(event.UserState.Badges)),
			slog.Int("emote_set_count", len(event.UserState.EmoteSets)),
			slog.Int("raw_tag_count", len(event.UserState.RawTags)),
		)
	case twitch.EventConnection:
		attrs = append(attrs, twitchConnectionEventDebugAttrs(event.Connection)...)
	case twitch.EventRaw:
		attrs = append(attrs,
			slog.String("raw_type", event.Raw.RawType),
			slog.Int("text_length", len([]rune(event.Raw.Text))),
			slog.Int("raw_length", len([]rune(event.Raw.Raw))),
			slog.Int("raw_tag_count", len(event.Raw.RawTags)),
			slog.Bool("todo_present", event.Raw.TODO != ""),
		)
	}
	return attrs
}

func twitchConnectionEventDebugAttrs(event twitch.ConnectionEvent) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("connection_type", string(event.Type)),
		slog.String("reason", event.Reason),
		slog.Bool("has_error", event.Err != nil),
	}
	if event.Err != nil {
		attrs = append(attrs, slog.String("error", event.Err.Error()))
	}
	if !event.At.IsZero() {
		attrs = append(attrs, slog.Time("at", event.At))
	}
	return attrs
}

func sendRequestDebugAttrs(req SendRequest) []slog.Attr {
	return []slog.Attr{
		slog.String("channel", req.Channel),
		slog.Bool("is_reply", req.ReplyToMessageID != ""),
		slog.Bool("is_action", req.Action),
		slog.String("reply_to_message_id", req.ReplyToMessageID),
		slog.Int("text_length", len([]rune(req.Text))),
	}
}

func queuedSendDebugAttrs(send queuedComposerSend) []slog.Attr {
	return []slog.Attr{
		slog.Int("send_id", send.ID),
		slog.String("channel", send.Channel),
		slog.Bool("is_reply", send.ReplyToMessageID != ""),
		slog.Bool("is_action", send.Action),
		slog.String("reply_to_message_id", send.ReplyToMessageID),
		slog.Int("text_length", len([]rune(send.Text))),
		slog.Int("draft_length", len([]rune(send.Draft))),
	}
}

func sendResultDebugAttrs(result SendResult, err error) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("message_id", result.MessageID),
		slog.Bool("accepted", err == nil && !result.RateLimited),
		slog.Bool("rate_limited", result.RateLimited),
		slog.Int64("retry_after_ms", int64(result.RetryAfter/time.Millisecond)),
		slog.String("detail", result.Detail),
		slog.Bool("has_error", err != nil),
	}
	if !result.AcceptedAt.IsZero() {
		attrs = append(attrs, slog.Time("accepted_at", result.AcceptedAt))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	return attrs
}

func assetBatchDebugAttrs(results []assetPreparedMsg) []slog.Attr {
	var cacheHits, downloaded, canceled, failed, permanent, rendered int
	for _, result := range results {
		switch result.event.Kind {
		case assets.EventCacheHit:
			cacheHits++
		case assets.EventDownloaded:
			downloaded++
		case assets.EventCanceled:
			canceled++
		case assets.EventFailed:
			failed++
		}
		if result.permanent || isPermanentAssetFailure(result) {
			permanent++
		}
		if result.err != nil || result.event.Err != nil {
			failed++
		}
		if result.cell.Text != "" {
			rendered++
		}
	}
	return []slog.Attr{
		slog.Int("result_count", len(results)),
		slog.Int("cache_hit_count", cacheHits),
		slog.Int("downloaded_count", downloaded),
		slog.Int("canceled_count", canceled),
		slog.Int("failed_count", failed),
		slog.Int("permanent_failure_count", permanent),
		slog.Int("rendered_count", rendered),
	}
}
