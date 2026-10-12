package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/history"
	"github.com/worxbend/twi/internal/twitch"
)

// HistoryChatSource attaches read-only to an explicitly running local recorder.
// It replays bounded per-channel scrollback and follows local sequence numbers;
// it never opens a Twitch connection or starts a recorder.
type HistoryChatSource struct {
	messages    chan twitch.ChatMessage
	states      chan ConnectionState
	moderations chan twitch.ModerationEvent
	cancel      context.CancelFunc
	done        chan struct{}
	once        sync.Once
}

var _ ChatSource = (*HistoryChatSource)(nil)
var _ ModerationSource = (*HistoryChatSource)(nil)

func NewHistoryChatSource(ctx context.Context, path string, channels []string, tail int) (*HistoryChatSource, error) {
	selected, err := twitch.ParseChannels(channels)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("history attachment requires explicit channels")
	}
	channels = make([]string, len(selected))
	for i, c := range selected {
		channels[i] = c.String()
	}
	if tail <= 0 {
		tail = 200
	}
	tail = min(tail, 10000)
	store, err := history.Open(path)
	if err != nil {
		return nil, err
	}
	query := history.Query{Channels: channels, Last: tail, AllEvents: true, AllowPartial: true, TimeField: "received"}
	selection, err := store.TailBoundary(ctx, query)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	query.Last = 0
	query.ChannelStarts = selection.Starts
	run, cancel := context.WithCancel(ctx)
	source := &HistoryChatSource{messages: make(chan twitch.ChatMessage, 128), states: make(chan ConnectionState, 128), moderations: make(chan twitch.ModerationEvent, 128), cancel: cancel, done: make(chan struct{})}
	go source.run(run, store, query, selection.HighWater)
	return source, nil
}
func (c *HistoryChatSource) Messages() <-chan twitch.ChatMessage        { return c.messages }
func (c *HistoryChatSource) ConnectionStates() <-chan ConnectionState   { return c.states }
func (c *HistoryChatSource) Moderations() <-chan twitch.ModerationEvent { return c.moderations }
func (c *HistoryChatSource) Close() error                               { c.once.Do(c.cancel); <-c.done; return nil }
func (c *HistoryChatSource) run(ctx context.Context, store *history.Store, query history.Query, boundary int64) {
	defer close(c.done)
	defer close(c.messages)
	defer close(c.states)
	defer close(c.moderations)
	defer store.Close()
	state := func(value ConnectionState) { emitConnectionState(ctx, c.states, value) }
	fail := func(err error) {
		state(ConnectionState{Status: ConnectionFailed, Detail: "local history read failed", Err: err, At: time.Now()})
	}
	emit := func(record automation.Record) bool {
		current, visible, err := store.Refresh(ctx, record, true)
		if err != nil {
			fail(err)
			return false
		}
		if !visible {
			return true
		}
		record = current
		if record.Type == "message" && record.Deleted {
			return true
		}
		switch twitch.ModerationType(record.Type) {
		case twitch.ModerationAction, twitch.ModerationChatCleared, twitch.ModerationUserMessagesCleared, twitch.ModerationUserBanned, twitch.ModerationUserTimedOut, twitch.ModerationMessageDeleted:
			event := historyModerationEvent(record)
			select {
			case c.moderations <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		message := historyChatMessage(record)
		select {
		case c.messages <- message:
			return true
		case <-ctx.Done():
			return false
		}
	}
	after := int64(0)
	for after < boundary {
		records, next, err := store.ReadThrough(ctx, query, after, boundary, 128)
		if err != nil {
			fail(err)
			return
		}
		for _, record := range records {
			if !emit(record) {
				return
			}
		}
		after = next
	}
	after = boundary
	state(ConnectionState{Status: ConnectionConnected, Detail: "local history replay complete; read-only follow; capture coverage may be partial", At: time.Now()})
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	health := time.NewTicker(2 * time.Second)
	defer health.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			records, next, err := store.ReadAfter(ctx, query, after, 128)
			if err != nil {
				fail(err)
				return
			}
			for _, record := range records {
				if !emit(record) {
					return
				}
			}
			after = next
		case <-health.C:
			status, err := store.Status(ctx)
			if err != nil {
				fail(err)
				return
			}
			for _, channel := range query.Channels {
				healthy := false
				for _, capture := range status.Captures {
					if capture.Channel == channel && capture.Healthy {
						healthy = true
						break
					}
				}
				connection := ConnectionDisconnected
				detail := "local history recorder unavailable; waiting for new local records"
				if healthy {
					connection = ConnectionConnected
					detail = "local history recorder active; read-only follow"
				}
				state(ConnectionState{Status: connection, Channel: channel, Detail: detail, At: time.Now()})
			}
		}
	}
}
func historyChatMessage(record automation.Record) twitch.ChatMessage {
	kind := twitch.MessageTypeChat
	switch twitch.MessageType(record.MessageKind) {
	case twitch.MessageTypeAction, twitch.MessageTypeNotice, twitch.MessageTypeSystem:
		kind = twitch.MessageType(record.MessageKind)
	}
	if record.Type == "notice" {
		kind = twitch.MessageTypeNotice
	}
	if record.Type == "user_notice" {
		kind = twitch.MessageTypeNotice
		if record.SystemText != "" && record.Text != record.SystemText {
			record.Text = strings.TrimSpace(strings.Join(nonEmptyStrings(record.SystemText, record.Text), " "))
		}
	}
	message := twitch.ChatMessage{AuthorColor: record.AuthorColor, Bits: record.Bits, FirstMessage: record.FirstMessage, PlatformMessageType: record.PlatformMessageType, ID: record.ID, Channel: record.Channel.Login, ChannelID: record.Channel.ID, Timestamp: record.EventAt, AuthorID: record.Author.ID, AuthorLogin: record.Author.Login, DisplayName: record.Author.DisplayName, Text: record.Text, Type: kind, DeliveryID: record.DeliveryID, SourceMessageID: record.SourceMessageID, SystemEventID: record.Reason}
	for _, badge := range record.Badges {
		message.Badges = append(message.Badges, twitch.Badge{SetID: badge.SetID, ID: badge.ID, Info: badge.Info})
	}
	for _, badge := range record.SourceBadges {
		message.SourceBadges = append(message.SourceBadges, twitch.Badge{SetID: badge.SetID, ID: badge.ID, Info: badge.Info})
	}
	if record.SourceChannel != nil {
		message.SourceChannelID = record.SourceChannel.ID
		message.SourceChannelLogin = record.SourceChannel.Login
	}
	if record.Reply != nil {
		message.Reply = &twitch.Reply{ParentMessageID: record.Reply.ParentID, ParentLogin: record.Reply.AuthorLogin, ParentText: record.Reply.Text}
	}
	for _, fragment := range record.Fragments {
		message.Fragments = append(message.Fragments, twitch.MessageFragment{Type: twitch.FragmentType(fragment.Type), Text: fragment.Text, Ref: twitch.AssetRef{ID: fragment.ID}, Bits: fragment.Bits, Tier: fragment.Tier})
	}
	if record.Type == "user_notice" && record.SystemText != "" && record.SystemText != record.Text {
		message.Fragments = noticeDisplayFragments(record.SystemText, record.Text, message.Fragments)
	}
	return message
}

func historyModerationEvent(record automation.Record) twitch.ModerationEvent {
	event := twitch.ModerationEvent{Type: twitch.ModerationType(record.Type), Channel: record.Channel.Login, RoomID: record.Channel.ID, Timestamp: record.EventAt, TargetUserID: record.TargetUserID, TargetLogin: record.Author.Login, TargetMessageID: record.TargetMessageID, DeliveryID: record.DeliveryID, Action: record.Action, Reason: record.Reason, Text: record.Text, TargetName: record.Author.DisplayName}
	if record.Moderator != nil {
		event.ModeratorID = record.Moderator.ID
		event.ModeratorLogin = record.Moderator.Login
		event.ModeratorName = record.Moderator.DisplayName
	}
	event.Parameters = map[string]string{}
	for key, value := range record.ModerationParameters {
		event.Parameters[key] = value
	}
	if record.BanDurationMS > 0 && record.BanDurationMS <= int64((1<<63-1)/time.Millisecond) {
		event.BanDuration = time.Duration(record.BanDurationMS) * time.Millisecond
	}
	if record.TimeoutExpiresAt != nil {
		event.ExpiresAt = *record.TimeoutExpiresAt
	}
	if record.SourceChannel != nil {
		event.SourceChannelID = record.SourceChannel.ID
		event.SourceChannelLogin = record.SourceChannel.Login
	}
	return event
}
