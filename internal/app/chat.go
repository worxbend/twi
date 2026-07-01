package app

import (
	"context"
	"time"

	"github.com/w0rxbend/twi/internal/twitch"
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
	MessageID  string
	AcceptedAt time.Time
}
