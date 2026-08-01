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

// UserStateSource is an optional ChatClient capability exposing Twitch
// USERSTATE for the authenticated user.
//
// It matters because Twitch never echoes a user's own PRIVMSG back to them:
// twi renders its own sent messages from a local echo, and USERSTATE is the
// only place that echo can learn the sender's badges from.
type UserStateSource interface {
	UserStates() <-chan twitch.UserState
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
