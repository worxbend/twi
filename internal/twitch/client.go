package twitch

import (
	"context"
	"time"
)

type ChatClient interface {
	Connect(ctx context.Context) (<-chan Event, error)
	Send(ctx context.Context, channel, text string) error
	Reply(ctx context.Context, channel, parentMessageID, text string) error
	Close() error
}

type Event struct {
	Kind       EventKind
	Message    ChatMessage
	Notice     Notice
	UserNotice UserNotice
	RoomState  RoomState
	Moderation ModerationEvent
	UserState  UserState
	Connection ConnectionEvent
	Raw        RawEvent
	Err        error
}

type EventKind string

const (
	EventConnected    EventKind = "connected"
	EventDisconnected EventKind = "disconnected"
	EventMessage      EventKind = "message"
	EventNotice       EventKind = "notice"
	EventUserNotice   EventKind = "user_notice"
	EventRoomState    EventKind = "room_state"
	EventModeration   EventKind = "moderation"
	EventUserState    EventKind = "user_state"
	EventConnection   EventKind = "connection"
	EventRaw          EventKind = "raw"
	EventClear        EventKind = "clear"
	EventError        EventKind = "error"
)

type Notice struct {
	Channel string
	ID      string
	Text    string
	// RawTags is retained only for diagnostics/debug views.
	RawTags map[string]string
}

type UserNotice struct {
	ID          string
	Channel     string
	RoomID      string
	Timestamp   time.Time
	AuthorLogin string
	AuthorID    string
	DisplayName string
	AuthorColor string
	Badges      []Badge
	Text        string
	SystemText  string
	MessageID   string
	Params      map[string]string
	Emotes      []Emote
	Fragments   []MessageFragment
	// RawTags is retained only for diagnostics/debug views.
	RawTags map[string]string
}

type RoomState struct {
	Channel string
	RoomID  string
	State   map[string]int
	// RawTags is retained only for diagnostics/debug views.
	RawTags map[string]string
}

type ModerationEvent struct {
	Type            ModerationType
	Channel         string
	RoomID          string
	Timestamp       time.Time
	TargetUserID    string
	TargetLogin     string
	TargetMessageID string
	BanDuration     time.Duration
	Text            string
	// RawTags is retained only for diagnostics/debug views.
	RawTags map[string]string
}

type ModerationType string

const (
	ModerationChatCleared    ModerationType = "chat_cleared"
	ModerationUserBanned     ModerationType = "user_banned"
	ModerationUserTimedOut   ModerationType = "user_timed_out"
	ModerationMessageDeleted ModerationType = "message_deleted"
)

type UserState struct {
	Channel     string
	AuthorLogin string
	AuthorID    string
	DisplayName string
	AuthorColor string
	Badges      []Badge
	EmoteSets   []string
	// RawTags is retained only for diagnostics/debug views.
	RawTags map[string]string
}

type ConnectionEvent struct {
	Type   ConnectionEventType
	At     time.Time
	Reason string
	Err    error
}

type ConnectionEventType string

const (
	ConnectionEventConnect    ConnectionEventType = "connect"
	ConnectionEventReconnect  ConnectionEventType = "reconnect"
	ConnectionEventDisconnect ConnectionEventType = "disconnect"
)

type RawEvent struct {
	RawType string
	Text    string
	Raw     string
	// RawTags is retained only for diagnostics/debug views.
	RawTags map[string]string
	TODO    string
}
