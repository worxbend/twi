package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/w0rxbend/twi/internal/auth"
	"github.com/w0rxbend/twi/internal/debuglog"
	"github.com/w0rxbend/twi/internal/twitch"
)

const defaultLiveChatBuffer = 128

var credentialRedactor = auth.NewRedactor()

// LiveChatClient adapts the transport-level Twitch chat client into the
// app-facing ChatClient interface consumed by the Bubble Tea model.
type LiveChatClient struct {
	factory LiveChatTransportFactory
	baseCtx context.Context

	messages chan twitch.ChatMessage
	states   chan ConnectionState
	done     chan struct{}
	closed   chan struct{}

	mu           sync.RWMutex
	session      *liveChatSession
	closedFlag   bool
	reconnecting bool
	lifecycleMu  sync.Mutex
	closeOnce    sync.Once
	debugLogger  debuglog.Logger
}

var _ ChatClient = (*LiveChatClient)(nil)

// LiveChatTransportFactory creates a fresh Twitch chat transport for a live
// chat session. It is called once for initial connection and again for each
// manual reconnect restart.
type LiveChatTransportFactory func(context.Context) (twitch.ChatClient, error)

type LiveChatClientOptions struct {
	DebugLogger debuglog.Logger
}

var (
	ErrReconnectUnavailable = errors.New("manual reconnect unavailable for this chat source")
	ErrReconnectInProgress  = errors.New("manual reconnect already in progress")
	ErrLiveChatClientClosed = errors.New("live chat client closed")
	ErrLiveChatDisconnected = errors.New("live chat client disconnected; reconnect with ctrl+r")
)

func NewLiveChatClient(ctx context.Context, transport twitch.ChatClient, buffer int) (*LiveChatClient, error) {
	return NewLiveChatClientWithOptions(ctx, transport, buffer, LiveChatClientOptions{})
}

func NewLiveChatClientWithOptions(ctx context.Context, transport twitch.ChatClient, buffer int, opts LiveChatClientOptions) (*LiveChatClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if transport == nil {
		return nil, errors.New("missing Twitch chat transport")
	}
	factory := func(context.Context) (twitch.ChatClient, error) {
		return transport, nil
	}
	return newLiveChatClient(ctx, nil, factory, buffer, opts)
}

// NewRestartableLiveChatClient creates a live client whose Reconnect method
// tears down the active transport and starts a fresh transport from factory.
func NewRestartableLiveChatClient(ctx context.Context, factory LiveChatTransportFactory, buffer int) (*LiveChatClient, error) {
	return NewRestartableLiveChatClientWithOptions(ctx, factory, buffer, LiveChatClientOptions{})
}

func NewRestartableLiveChatClientWithOptions(ctx context.Context, factory LiveChatTransportFactory, buffer int, opts LiveChatClientOptions) (*LiveChatClient, error) {
	if factory == nil {
		return nil, errors.New("missing Twitch chat transport factory")
	}
	return newLiveChatClient(ctx, factory, factory, buffer, opts)
}

func newLiveChatClient(ctx context.Context, reconnectFactory, initialFactory LiveChatTransportFactory, buffer int, opts LiveChatClientOptions) (*LiveChatClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if buffer <= 0 {
		buffer = defaultLiveChatBuffer
	}

	client := &LiveChatClient{
		factory:     reconnectFactory,
		baseCtx:     ctx,
		messages:    make(chan twitch.ChatMessage, buffer),
		states:      make(chan ConnectionState, buffer),
		done:        make(chan struct{}),
		closed:      make(chan struct{}),
		debugLogger: opts.DebugLogger,
	}
	client.debugLiveEvent("live_chat.start", slog.Int("buffer", buffer), slog.Bool("restartable", reconnectFactory != nil))
	client.emitState(ctx, ConnectionState{
		Status: ConnectionConnecting,
		Detail: "connecting to Twitch IRC",
		At:     time.Now(),
	})

	session, err := client.newSession(ctx, initialFactory)
	if err != nil {
		safeErr := errors.New(credentialSafeDetail(err))
		client.debugLiveEvent("live_chat.start.failed", slog.String("error", safeErr.Error()))
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

	client.setSession(session)
	go client.bridge(session)
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
	c.debugLiveSendRequest(req)
	transport, err := c.currentTransport()
	if err != nil {
		c.debugLiveSendComplete(req, SendResult{}, err)
		return SendResult{}, err
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		err := errors.New("message text cannot be empty")
		c.debugLiveSendComplete(req, SendResult{}, err)
		return SendResult{}, err
	}
	if req.Action {
		text = actionWireText(text)
	}
	if req.ReplyToMessageID != "" {
		if err := transport.Reply(ctx, req.Channel, req.ReplyToMessageID, text); err != nil {
			safeErr := errors.New(credentialSafeSendDetail(err))
			c.debugLiveSendComplete(req, SendResult{}, safeErr)
			return SendResult{}, safeErr
		}
		result := SendResult{AcceptedAt: time.Now()}
		c.debugLiveSendComplete(req, result, nil)
		return result, nil
	}
	if err := transport.Send(ctx, req.Channel, text); err != nil {
		safeErr := errors.New(credentialSafeSendDetail(err))
		c.debugLiveSendComplete(req, SendResult{}, safeErr)
		return SendResult{}, safeErr
	}
	result := SendResult{AcceptedAt: time.Now()}
	c.debugLiveSendComplete(req, result, nil)
	return result, nil
}

func actionWireText(text string) string {
	return "\x01ACTION " + strings.TrimSpace(text) + "\x01"
}

func (c *LiveChatClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.lifecycleMu.Lock()
		c.mu.Lock()
		c.closedFlag = true
		session := c.session
		c.session = nil
		close(c.done)
		c.mu.Unlock()
		err = session.stop(true)
		c.lifecycleMu.Unlock()
		close(c.messages)
		close(c.states)
		close(c.closed)
	})
	return err
}

func (c *LiveChatClient) Reconnect(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.factory == nil {
		return ErrReconnectUnavailable
	}
	if !c.lifecycleMu.TryLock() {
		return ErrReconnectInProgress
	}
	defer c.lifecycleMu.Unlock()

	if err := c.ensureOpen(); err != nil {
		return err
	}
	c.setReconnecting(true)
	defer c.setReconnecting(false)

	c.emitState(ctx, ConnectionState{
		Status: ConnectionReconnecting,
		Detail: "manual reconnect restarting Twitch IRC",
		At:     time.Now(),
	})
	c.debugLiveEvent("live_chat.reconnect.start")

	old := c.swapSession(nil)
	if err := old.stop(true); err != nil {
		safeErr := errors.New(credentialSafeDetail(err))
		c.debugLiveEvent("live_chat.reconnect.failed", slog.String("error", safeErr.Error()))
		return safeErr
	}
	if err := ctx.Err(); err != nil {
		c.emitState(context.Background(), ConnectionState{
			Status: ConnectionFailed,
			Detail: "manual reconnect canceled; retry with ctrl+r",
			Err:    err,
			At:     time.Now(),
		})
		c.debugLiveEvent("live_chat.reconnect.failed", slog.String("error", err.Error()))
		return err
	}

	session, err := c.newSession(ctx, c.factory)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			c.emitState(context.Background(), ConnectionState{
				Status: ConnectionFailed,
				Detail: "manual reconnect canceled; retry with ctrl+r",
				Err:    err,
				At:     time.Now(),
			})
			c.debugLiveEvent("live_chat.reconnect.failed", slog.String("error", err.Error()))
			return err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			c.emitState(context.Background(), ConnectionState{
				Status: ConnectionFailed,
				Detail: "manual reconnect timed out; retry with ctrl+r",
				Err:    err,
				At:     time.Now(),
			})
			c.debugLiveEvent("live_chat.reconnect.failed", slog.String("error", err.Error()))
			return err
		}
		safeErr := errors.New(credentialSafeDetail(err))
		c.emitState(ctx, ConnectionState{
			Status: ConnectionFailed,
			Detail: "manual reconnect failed: " + safeErr.Error() + "; retry with ctrl+r",
			Err:    safeErr,
			At:     time.Now(),
		})
		c.debugLiveEvent("live_chat.reconnect.failed", slog.String("error", safeErr.Error()))
		return safeErr
	}
	if err := c.ensureOpen(); err != nil {
		_ = session.stop(true)
		c.debugLiveEvent("live_chat.reconnect.failed", slog.String("error", err.Error()))
		return err
	}
	c.setSession(session)
	go c.bridge(session)
	c.debugLiveEvent("live_chat.reconnect.session_started")
	return nil
}

func (c *LiveChatClient) bridge(session *liveChatSession) {
	defer close(session.done)

	terminalStateSeen := false
	for {
		select {
		case event, ok := <-session.events:
			if session.suppressed() {
				return
			}
			if !ok {
				if terminalStateSeen {
					return
				}
				c.emitState(session.ctx, ConnectionState{
					Status: ConnectionDisconnected,
					Detail: "Twitch IRC connection closed",
					At:     time.Now(),
				})
				return
			}
			c.handleEvent(session.ctx, event)
			if isTerminalEvent(event) {
				terminalStateSeen = true
			}
		case <-session.ctx.Done():
			if !session.suppressed() {
				c.emitState(context.Background(), ConnectionState{
					Status: ConnectionClosed,
					Detail: "chat session canceled",
					Err:    session.ctx.Err(),
					At:     time.Now(),
				})
			}
			return
		case <-c.done:
			return
		}
	}
}

func (c *LiveChatClient) newSession(ctx context.Context, factory LiveChatTransportFactory) (*liveChatSession, error) {
	if factory == nil {
		return nil, ErrReconnectUnavailable
	}
	c.debugLiveEvent("live_chat.session.create")
	transport, err := factory(ctx)
	if err != nil {
		c.debugLiveEvent("live_chat.session.create_failed", slog.String("error", credentialSafeDetail(err)))
		return nil, err
	}
	if transport == nil {
		err := errors.New("missing Twitch chat transport")
		c.debugLiveEvent("live_chat.session.create_failed", slog.String("error", err.Error()))
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = transport.Close()
		c.debugLiveEvent("live_chat.session.create_failed", slog.String("error", err.Error()))
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(c.baseCtx)
	session := &liveChatSession{
		ctx:       sessionCtx,
		cancel:    cancel,
		transport: transport,
		done:      make(chan struct{}),
	}
	c.debugLiveEvent("live_chat.transport.connect.start")
	events, err := connectTransport(ctx, sessionCtx, transport)
	if err != nil {
		cancel()
		_ = transport.Close()
		c.debugLiveEvent("live_chat.transport.connect_failed", slog.String("error", credentialSafeDetail(err)))
		return nil, err
	}
	session.events = events
	c.debugLiveEvent("live_chat.transport.connect_started")
	return session, nil
}

type connectResult struct {
	events <-chan twitch.Event
	err    error
}

func connectTransport(ctx, sessionCtx context.Context, transport twitch.ChatClient) (<-chan twitch.Event, error) {
	resultCh := make(chan connectResult, 1)
	go func() {
		events, err := transport.Connect(sessionCtx)
		resultCh <- connectResult{events: events, err: err}
	}()

	select {
	case result := <-resultCh:
		return result.events, result.err
	case <-ctx.Done():
		_ = transport.Close()
		return nil, ctx.Err()
	}
}

func (c *LiveChatClient) currentTransport() (twitch.ChatClient, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closedFlag {
		return nil, ErrLiveChatClientClosed
	}
	if c.session == nil || c.session.transport == nil {
		if c.reconnecting {
			return nil, ErrReconnectInProgress
		}
		return nil, ErrLiveChatDisconnected
	}
	return c.session.transport, nil
}

func (c *LiveChatClient) ensureOpen() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closedFlag {
		return ErrLiveChatClientClosed
	}
	return nil
}

func (c *LiveChatClient) setSession(session *liveChatSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = session
}

func (c *LiveChatClient) swapSession(session *liveChatSession) *liveChatSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.session
	c.session = session
	return old
}

func (c *LiveChatClient) setReconnecting(reconnecting bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconnecting = reconnecting
}

type liveChatSession struct {
	ctx       context.Context
	cancel    context.CancelFunc
	transport twitch.ChatClient
	events    <-chan twitch.Event
	done      chan struct{}

	mu                 sync.RWMutex
	suppressCloseState bool
}

func (s *liveChatSession) stop(suppressCloseState bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if suppressCloseState {
		s.suppressCloseState = true
	}
	s.mu.Unlock()
	s.cancel()
	err := s.transport.Close()
	<-s.done
	return err
}

func (s *liveChatSession) suppressed() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.suppressCloseState
}

func (c *LiveChatClient) handleEvent(ctx context.Context, event twitch.Event) {
	c.debugTransportEvent(event)
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
		RawTags:   notice.RawTags,
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
		ChannelID:   notice.RoomID,
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
	redacted := redactCredentialText(err.Error())
	lower := strings.ToLower(redacted)
	if strings.Contains(lower, "auth") || strings.Contains(lower, "login") || strings.Contains(lower, "scope") {
		return "Twitch IRC authentication failed; verify username, OAuth token, and chat:read scope"
	}
	return redacted
}

func credentialSafeSendDetail(err error) string {
	if err == nil {
		return ""
	}
	redacted := redactCredentialText(err.Error())
	lower := strings.ToLower(redacted)
	if strings.Contains(lower, "auth") || strings.Contains(lower, "login") || strings.Contains(lower, "scope") || strings.Contains(lower, "permission") {
		return "Twitch IRC send failed; verify username, OAuth token, and chat:edit scope"
	}
	return redacted
}

func detailOrFallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return redactCredentialText(value)
}

func redactCredentialText(value string) string {
	return credentialRedactor.Redact(value)
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
