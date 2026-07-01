package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/w0rxbend/twi/internal/twitch"
)

const defaultLiveChatBuffer = 128

var oauthTokenPattern = regexp.MustCompile(`(?i)oauth:[^\s]+`)

// LiveChatClient adapts the transport-level Twitch chat client into the
// app-facing ChatClient interface consumed by the Bubble Tea model.
type LiveChatClient struct {
	transport twitch.ChatClient
	messages  chan twitch.ChatMessage
	states    chan ConnectionState
	done      chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

var _ ChatClient = (*LiveChatClient)(nil)

func NewLiveChatClient(ctx context.Context, transport twitch.ChatClient, buffer int) (*LiveChatClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if transport == nil {
		return nil, errors.New("missing Twitch chat transport")
	}
	if buffer <= 0 {
		buffer = defaultLiveChatBuffer
	}

	client := &LiveChatClient{
		transport: transport,
		messages:  make(chan twitch.ChatMessage, buffer),
		states:    make(chan ConnectionState, buffer),
		done:      make(chan struct{}),
		closed:    make(chan struct{}),
	}
	client.emitState(ctx, ConnectionState{
		Status: ConnectionConnecting,
		Detail: "connecting to Twitch IRC",
		At:     time.Now(),
	})

	events, err := transport.Connect(ctx)
	if err != nil {
		safeErr := errors.New(credentialSafeDetail(err))
		client.emitState(ctx, ConnectionState{
			Status: ConnectionFailed,
			Detail: safeErr.Error(),
			Err:    safeErr,
			At:     time.Now(),
		})
		close(client.messages)
		close(client.states)
		close(client.closed)
		return nil, safeErr
	}

	go client.bridge(ctx, events)
	return client, nil
}

func (c *LiveChatClient) Messages() <-chan twitch.ChatMessage {
	return c.messages
}

func (c *LiveChatClient) ConnectionStates() <-chan ConnectionState {
	return c.states
}

func (c *LiveChatClient) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	if err := ctx.Err(); err != nil {
		return SendResult{}, err
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return SendResult{}, errors.New("message text cannot be empty")
	}
	if req.ReplyToMessageID != "" {
		if err := c.transport.Reply(ctx, req.Channel, req.ReplyToMessageID, text); err != nil {
			return SendResult{}, errors.New(credentialSafeSendDetail(err))
		}
		return SendResult{AcceptedAt: time.Now()}, nil
	}
	if err := c.transport.Send(ctx, req.Channel, text); err != nil {
		return SendResult{}, errors.New(credentialSafeSendDetail(err))
	}
	return SendResult{AcceptedAt: time.Now()}, nil
}

func (c *LiveChatClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.transport.Close()
		<-c.closed
	})
	return err
}

func (c *LiveChatClient) bridge(ctx context.Context, events <-chan twitch.Event) {
	defer close(c.closed)
	defer close(c.messages)
	defer close(c.states)

	terminalStateSeen := false
	for {
		select {
		case event, ok := <-events:
			if !ok {
				if terminalStateSeen {
					return
				}
				c.emitState(ctx, ConnectionState{
					Status: ConnectionDisconnected,
					Detail: "Twitch IRC connection closed",
					At:     time.Now(),
				})
				return
			}
			c.handleEvent(ctx, event)
			if isTerminalEvent(event) {
				terminalStateSeen = true
			}
		case <-ctx.Done():
			c.emitState(ctx, ConnectionState{
				Status: ConnectionClosed,
				Detail: "chat session canceled",
				Err:    ctx.Err(),
				At:     time.Now(),
			})
			return
		case <-c.done:
			return
		}
	}
}

func (c *LiveChatClient) handleEvent(ctx context.Context, event twitch.Event) {
	switch event.Kind {
	case twitch.EventMessage:
		c.emitMessage(ctx, event.Message)
	case twitch.EventNotice:
		c.emitMessage(ctx, messageFromNotice(event.Notice))
		c.emitState(ctx, stateFromNotice(event.Notice))
	case twitch.EventUserNotice:
		c.emitMessage(ctx, messageFromUserNotice(event.UserNotice))
	case twitch.EventModeration:
		c.emitMessage(ctx, messageFromModeration(event.Moderation))
	case twitch.EventConnection:
		c.emitState(ctx, stateFromConnectionEvent(event.Connection))
	case twitch.EventError:
		c.emitState(ctx, ConnectionState{
			Status: ConnectionFailed,
			Detail: credentialSafeDetail(event.Err),
			Err:    event.Err,
			At:     time.Now(),
		})
	}
	if event.Err != nil && event.Kind != twitch.EventError && event.Kind != twitch.EventConnection {
		c.emitState(ctx, ConnectionState{
			Status: ConnectionFailed,
			Detail: credentialSafeDetail(event.Err),
			Err:    event.Err,
			At:     time.Now(),
		})
	}
}

func (c *LiveChatClient) emitMessage(ctx context.Context, msg twitch.ChatMessage) {
	select {
	case c.messages <- msg:
	case <-ctx.Done():
	case <-c.done:
	}
}

func (c *LiveChatClient) emitState(ctx context.Context, state ConnectionState) {
	select {
	case c.states <- state:
	case <-ctx.Done():
	case <-c.done:
	}
}

func stateFromConnectionEvent(event twitch.ConnectionEvent) ConnectionState {
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	switch event.Type {
	case twitch.ConnectionEventConnect:
		return ConnectionState{
			Status: ConnectionConnected,
			Detail: "joined Twitch IRC",
			At:     at,
		}
	case twitch.ConnectionEventReconnect:
		return ConnectionState{
			Status: ConnectionReconnecting,
			Detail: detailOrFallback(event.Reason, "Twitch requested reconnect"),
			At:     at,
		}
	case twitch.ConnectionEventDisconnect:
		state := ConnectionState{
			Status: ConnectionDisconnected,
			Detail: "Twitch IRC disconnected",
			Err:    event.Err,
			At:     at,
		}
		if event.Err != nil {
			state.Status = ConnectionFailed
			state.Detail = credentialSafeDetail(event.Err)
		}
		return state
	default:
		return ConnectionState{
			Status: ConnectionDisconnected,
			Detail: "Twitch IRC state changed",
			At:     at,
		}
	}
}

func stateFromNotice(notice twitch.Notice) ConnectionState {
	detail := detailOrFallback(notice.Text, "Twitch notice")
	status := ConnectionConnected
	if isAuthNotice(notice) {
		status = ConnectionFailed
		detail = "Twitch IRC authentication failed; verify username, OAuth token, and chat:read scope"
	}
	return ConnectionState{
		Status:  status,
		Channel: notice.Channel,
		Detail:  detail,
		At:      time.Now(),
	}
}

func messageFromNotice(notice twitch.Notice) twitch.ChatMessage {
	text := redactCredentialText(detailOrFallback(notice.Text, "Twitch notice"))
	return twitch.ChatMessage{
		ID:        notice.ID,
		Channel:   notice.Channel,
		Timestamp: time.Now(),
		Text:      text,
		Type:      twitch.MessageTypeNotice,
	}
}

func messageFromUserNotice(notice twitch.UserNotice) twitch.ChatMessage {
	text := strings.TrimSpace(strings.Join(nonEmptyStrings(notice.SystemText, notice.Text), " "))
	if text == "" {
		text = detailOrFallback(notice.MessageID, "Twitch user notice")
	}
	return twitch.ChatMessage{
		ID:          notice.ID,
		Channel:     notice.Channel,
		Timestamp:   notice.Timestamp,
		AuthorLogin: notice.AuthorLogin,
		AuthorID:    notice.AuthorID,
		DisplayName: notice.DisplayName,
		AuthorColor: notice.AuthorColor,
		Badges:      notice.Badges,
		Text:        text,
		Fragments:   notice.Fragments,
		Emotes:      notice.Emotes,
		Type:        twitch.MessageTypeNotice,
	}
}

func messageFromModeration(event twitch.ModerationEvent) twitch.ChatMessage {
	text := string(event.Type)
	if event.TargetLogin != "" {
		text = fmt.Sprintf("%s: %s", text, event.TargetLogin)
	}
	if event.TargetMessageID != "" {
		text = fmt.Sprintf("%s: %s", text, event.TargetMessageID)
	}
	if event.Text != "" {
		text = fmt.Sprintf("%s - %s", text, event.Text)
	}
	return twitch.ChatMessage{
		ID:        event.TargetMessageID,
		Channel:   event.Channel,
		Timestamp: event.Timestamp,
		Text:      text,
		Type:      twitch.MessageTypeNotice,
		Deleted:   event.Type == twitch.ModerationMessageDeleted,
	}
}

func isAuthNotice(notice twitch.Notice) bool {
	value := strings.ToLower(notice.ID + " " + notice.Text)
	return strings.Contains(value, "login") ||
		strings.Contains(value, "auth") ||
		strings.Contains(value, "invalid") ||
		strings.Contains(value, "permission") ||
		strings.Contains(value, "scope")
}

func isTerminalEvent(event twitch.Event) bool {
	if event.Kind == twitch.EventError {
		return true
	}
	return event.Kind == twitch.EventConnection && event.Connection.Type == twitch.ConnectionEventDisconnect
}

func credentialSafeDetail(err error) string {
	if err == nil {
		return ""
	}
	lower := strings.ToLower(oauthTokenPattern.ReplaceAllString(err.Error(), ""))
	if strings.Contains(lower, "auth") || strings.Contains(lower, "login") || strings.Contains(lower, "scope") {
		return "Twitch IRC authentication failed; verify username, OAuth token, and chat:read scope"
	}
	return redactCredentialText(err.Error())
}

func credentialSafeSendDetail(err error) string {
	if err == nil {
		return ""
	}
	lower := strings.ToLower(oauthTokenPattern.ReplaceAllString(err.Error(), ""))
	if strings.Contains(lower, "auth") || strings.Contains(lower, "login") || strings.Contains(lower, "scope") || strings.Contains(lower, "permission") {
		return "Twitch IRC send failed; verify username, OAuth token, and chat:edit scope"
	}
	return redactCredentialText(err.Error())
}

func detailOrFallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return redactCredentialText(value)
}

func redactCredentialText(value string) string {
	return oauthTokenPattern.ReplaceAllString(value, "oauth:<redacted>")
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
