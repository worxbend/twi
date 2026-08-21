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

// ChannelJoiner is an optional ChatClient capability: transports that can
// join and leave channels on an already-open connection implement it, so
// channels opened at runtime do not require a reconnect.
type ChannelJoiner interface {
	Join(channels ...string) error
	Depart(channel string) error
}

type Event struct {
	Kind       EventKind
	Message    ChatMessage
	Notice     Notice
	UserNotice UserNotice
	RoomState  RoomState
	Moderation ModerationEvent
	UserState  UserState
	Membership MembershipEvent
	Connection ConnectionEvent
	Raw        RawEvent
	Err        error
}

type EventKind string

// The kinds a transport may report. EventError is deliberately kept even
// though the IRC adapter never sets it: Event is the port type of ChatClient,
// not an adapter's private enum, and a future transport's failures reaching
// the user as silence is a worse outcome than one unused constant.
const (
	EventMessage    EventKind = "message"
	EventNotice     EventKind = "notice"
	EventUserNotice EventKind = "user_notice"
	EventRoomState  EventKind = "room_state"
	EventModeration EventKind = "moderation"
	EventUserState  EventKind = "user_state"
	EventMembership EventKind = "membership"
	EventConnection EventKind = "connection"
	EventRaw        EventKind = "raw"
	EventError      EventKind = "error"
)

type Notice struct {
	Channel string
	ID      string
	Text    string
	// AuthFailed marks a notice that means Twitch rejected the credentials,
	// as opposed to one describing channel or account state on a connection
	// that authenticated fine.
	//
	// The transport sets it, because recognising it requires knowing what
	// Twitch's login failures look like on the wire -- these arrive before
	// registration completes, on the "*" channel, with no msg-id, so the
	// message text is the only signal. Consumers act on the flag rather than
	// re-deriving it, which is the same rule ErrAuthFailed follows for
	// connection errors.
	AuthFailed bool
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

// MembershipEvent reports a user joining or leaving a channel's chat.
//
// Twitch delivers these through the twitch.tv/membership IRC capability as
// plain JOIN/PART lines with no tags, so a login is the only identity
// available - there is no user ID, display name, or badge information. Twitch
// also batches and delays membership for large channels and omits it entirely
// past a viewer threshold, so treat the resulting roster as a best-effort
// view of chat presence rather than an authoritative viewer list.
type MembershipEvent struct {
	Type    MembershipType
	Channel string
	Login   string
	At      time.Time
}

type MembershipType string

const (
	MembershipJoin MembershipType = "join"
	MembershipPart MembershipType = "part"
)

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
