package app

import (
	"context"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

// ChatClient is the app-facing boundary for chat input, connection state, and
// outbound sends. Implementations must emit normalized Twitch messages, not
// transport-specific IRC or API payloads.
type ChatClient interface {
	Messages() <-chan twitch.ChatMessage
	ConnectionStates() <-chan ConnectionState
	Send(context.Context, SendRequest) (SendResult, error)
	Close() error
}

// MembershipSource is an optional ChatClient capability: a transport that can
// observe Twitch's twitch.tv/membership JOIN/PART stream exposes it here so
// the model can track chat presence. It is deliberately kept off ChatClient
// because membership is best-effort - Twitch batches, delays, and eventually
// stops sending it for large channels - so every consumer must already cope
// with a transport that never reports any membership at all.
type MembershipSource interface {
	Memberships() <-chan twitch.MembershipEvent
}

// ChannelJoiner is an optional ChatClient capability: a transport that can
// join and part channels after connecting exposes it here, so channels
// opened from the /channels picker start receiving messages without a
// reconnect. Transports without it (the mock source, fakes) still work - the
// model simply tracks the channel locally.
type ChannelJoiner interface {
	JoinChannel(channel string) error
	PartChannel(channel string) error
}

// UserStateSource is an optional ChatClient capability exposing Twitch
// USERSTATE for the authenticated user.
//
// It matters because Twitch never echoes a user's own PRIVMSG back to them:
// twi renders its own sent messages from a local echo, and USERSTATE is the
// only place that echo can learn the sender's badges from.
type UserStateSource interface {
	UserStates() <-chan twitch.UserState
}

// ModerationSource is an optional ChatClient capability exposing moderation
// actions: deletions, timeouts, bans, and chat clears.
//
// It is deliberately not folded into the message stream. A moderation action
// is an instruction to remove text that is already on screen, so rendering it
// as another chat message puts the removed text back in front of the viewer -
// on a terminal that is frequently on stream. Consumers apply these to
// messages they already hold instead.
type ModerationSource interface {
	Moderations() <-chan twitch.ModerationEvent
}

type ConnectionState struct {
	Status  ConnectionStatus
	Channel string
	Detail  string
	Err     error
	At      time.Time
}

type ConnectionStatus string

const (
	ConnectionConnecting   ConnectionStatus = "connecting"
	ConnectionConnected    ConnectionStatus = "connected"
	ConnectionReconnecting ConnectionStatus = "reconnecting"
	ConnectionDisconnected ConnectionStatus = "disconnected"
	ConnectionClosed       ConnectionStatus = "closed"
	ConnectionFailed       ConnectionStatus = "failed"
)

type SendRequest struct {
	Channel          string
	Text             string
	ReplyToMessageID string
	Action           bool
}

type SendResult struct {
	MessageID   string
	AcceptedAt  time.Time
	Detail      string
	RateLimited bool
	RetryAfter  time.Duration
}
