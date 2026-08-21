package app

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/debuglog"
	"github.com/worxbend/twi/internal/twitch"
)

const defaultLiveChatBuffer = 128

const (
	// initialAutoReconnectDelay is short enough that a momentary blip is
	// invisible, long enough not to hammer a server that just closed us.
	initialAutoReconnectDelay = 2 * time.Second
	maxAutoReconnectDelay     = 60 * time.Second
	autoReconnectTimeout      = 30 * time.Second
	// maxAutoReconnectAttempts spans roughly ten minutes with the backoff
	// above, after which the failure is reported and ctrl+r takes over.
	maxAutoReconnectAttempts = 10
)

var credentialRedactor = auth.NewRedactor()

// LiveChatClient adapts the transport-level Twitch chat client into the
// app-facing ChatClient interface consumed by the Bubble Tea model.
type LiveChatClient struct {
	factory LiveChatTransportFactory
	baseCtx context.Context

	messages    chan twitch.ChatMessage
	states      chan ConnectionState
	memberships chan twitch.MembershipEvent
	userStates  chan twitch.UserState
	moderations chan twitch.ModerationEvent
	done        chan struct{}
	closed      chan struct{}

	droppedMessages atomic.Uint64

	mu           sync.RWMutex
	session      *liveChatSession
	closedFlag   bool
	reconnecting bool
	// sessionGen counts installed sessions. An auto-reconnect goroutine
	// remembers the generation it was spawned for and stands down once a
	// newer session exists, so it cannot tear down a connection somebody
	// else already restored.
	sessionGen  uint64
	lifecycleMu sync.Mutex
	closeOnce   sync.Once
	// autoReconnects tracks in-flight auto-reconnect goroutines so Close
	// can wait for them before closing the output channels they emit on.
	autoReconnects sync.WaitGroup
	debugLogger    debuglog.Logger
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
		memberships: make(chan twitch.MembershipEvent, buffer),
		userStates:  make(chan twitch.UserState, buffer),
		moderations: make(chan twitch.ModerationEvent, buffer),
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
		safeErr := credentialSafeError(err)
		client.debugLiveEvent("live_chat.start.failed", slog.String("error", safeErr.Error()))
		client.emitState(ctx, ConnectionState{
			Status: ConnectionFailed,
			Detail: safeErr.Error(),
			Err:    safeErr,
			At:     time.Now(),
		})
		close(client.messages)
		close(client.states)
		close(client.memberships)
		close(client.userStates)
		close(client.moderations)
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

// UserStates implements UserStateSource. Twitch sends USERSTATE on join and
// after each message the authenticated user sends; it is the only source of
// that user's own badges, because Twitch never echoes their PRIVMSGs back.
func (c *LiveChatClient) UserStates() <-chan twitch.UserState {
	return c.userStates
}

// Moderations implements ModerationSource. The channel stays open for the
// client's lifetime so callers can select on it unconditionally.
func (c *LiveChatClient) Moderations() <-chan twitch.ModerationEvent {
	return c.moderations
}

// Memberships implements MembershipSource. The channel stays open for the
// client's lifetime even when Twitch never sends membership for a channel,
// so callers can select on it unconditionally.
func (c *LiveChatClient) Memberships() <-chan twitch.MembershipEvent {
	return c.memberships
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
			safeErr := credentialSafeSendError(err)
			result := SendResult{RateLimited: isRateLimited(err), Detail: credentialSafeSendDetail(err)}
			c.debugLiveSendComplete(req, result, safeErr)
			return result, safeErr
		}
		result := SendResult{AcceptedAt: time.Now()}
		c.debugLiveSendComplete(req, result, nil)
		return result, nil
	}
	if err := transport.Send(ctx, req.Channel, text); err != nil {
		safeErr := credentialSafeSendError(err)
		result := SendResult{RateLimited: isRateLimited(err), Detail: credentialSafeSendDetail(err)}
		c.debugLiveSendComplete(req, result, safeErr)
		return result, safeErr
	}
	result := SendResult{AcceptedAt: time.Now()}
	c.debugLiveSendComplete(req, result, nil)
	return result, nil
}

// JoinChannel and PartChannel forward to the transport when it supports
// joining on a live connection, so a channel opened from the /channels
// picker starts receiving messages immediately. A transport without the
// capability reports an error the model treats as "tracked locally only".
func (c *LiveChatClient) JoinChannel(channel string) error {
	joiner, err := c.channelJoiner()
	if err != nil {
		return err
	}
	return joiner.Join(channel)
}

func (c *LiveChatClient) PartChannel(channel string) error {
	joiner, err := c.channelJoiner()
	if err != nil {
		return err
	}
	return joiner.Depart(channel)
}

func (c *LiveChatClient) channelJoiner() (twitch.ChannelJoiner, error) {
	transport, err := c.currentTransport()
	if err != nil {
		return nil, err
	}
	joiner, ok := transport.(twitch.ChannelJoiner)
	if !ok {
		return nil, errors.New("chat transport cannot join channels after connecting")
	}
	return joiner, nil
}

func actionWireText(text string) string {
	return "\x01ACTION " + strings.TrimSpace(text) + "\x01"
}

func (c *LiveChatClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		// Close c.done before reaching for lifecycleMu, not after.
		//
		// reconnect holds lifecycleMu for its whole body, and the failure
		// paths inside it emit a connection state on a background context.
		// Once Bubble Tea has returned there is nobody draining c.states, so
		// that emit blocks, and c.done is its only other way out. Taking
		// lifecycleMu first would mean waiting for a goroutine that is itself
		// waiting for this close -- an unbreakable hang on quit, in exactly
		// the window where RunClientWithOptions' deferred Close runs.
		//
		// Closing c.done first releases any such goroutine, so lifecycleMu
		// becomes available and the teardown below proceeds.
		c.mu.Lock()
		c.closedFlag = true
		close(c.done)
		c.mu.Unlock()

		c.lifecycleMu.Lock()
		c.mu.Lock()
		session := c.session
		c.session = nil
		c.mu.Unlock()
		err = session.stop(true)
		c.lifecycleMu.Unlock()
		// Wait before closing the output channels. An auto-reconnect
		// goroutine emits its terminal "gave up" state outside lifecycleMu;
		// if that emit raced this close, its select would have both a closed
		// c.done and a closed c.states ready and would panic with "send on
		// closed channel" about half the time. c.done is already closed
		// above, so every such goroutine is unblocked and returns promptly.
		c.autoReconnects.Wait()
		close(c.messages)
		close(c.states)
		close(c.memberships)
		close(c.userStates)
		close(c.moderations)
		close(c.closed)
	})
	return err
}

func (c *LiveChatClient) Reconnect(ctx context.Context) error {
	return c.reconnect(ctx, "manual")
}

// reconnect restarts the transport. kind distinguishes a user pressing ctrl+r
// from the automatic retry loop, and only changes the wording of the states
// it emits.
// logReconnectFailure records a failed reconnect in the debug log and hands
// the error back for the caller to return.
func (c *LiveChatClient) logReconnectFailure(err error) error {
	c.debugLiveEvent("live_chat.reconnect.failed", slog.String("error", err.Error()))
	return err
}

// failReconnect reports a reconnect failure to the user as well as the log:
// it puts the connection into the failed state with a reason and a way
// forward, then returns the error.
//
// The state is emitted on a background context rather than the reconnect's
// own, because the most common reason to be here is that context having been
// cancelled -- and emitting on a cancelled context would drop the very
// message that explains why chat stopped reconnecting.
func (c *LiveChatClient) failReconnect(detail string, err error) error {
	c.emitState(context.Background(), ConnectionState{
		Status: ConnectionFailed,
		Detail: detail,
		Err:    err,
		At:     time.Now(),
	})
	return c.logReconnectFailure(err)
}

func (c *LiveChatClient) reconnect(ctx context.Context, kind string) error {
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
		Detail: kind + " reconnect restarting Twitch IRC",
		At:     time.Now(),
	})
	c.debugLiveEvent("live_chat.reconnect.start")

	// Tear the old session down before starting a replacement, so the two
	// never hold a connection to Twitch at the same time.
	old := c.swapSession(nil)
	if err := old.stop(true); err != nil {
		return c.logReconnectFailure(credentialSafeError(err))
	}
	if err := ctx.Err(); err != nil {
		return c.failReconnect(kind+" reconnect canceled; retry with ctrl+r", err)
	}

	session, err := c.newSession(ctx, c.factory)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return c.failReconnect(kind+" reconnect canceled; retry with ctrl+r", err)
		case errors.Is(err, context.DeadlineExceeded):
			return c.failReconnect(kind+" reconnect timed out; retry with ctrl+r", err)
		default:
			safeErr := credentialSafeError(err)
			return c.failReconnect(kind+" reconnect failed: "+safeErr.Error()+"; retry with ctrl+r", safeErr)
		}
	}
	// The client can be closed while the replacement session is being built;
	// if it was, stop the session rather than installing it.
	if err := c.ensureOpen(); err != nil {
		_ = session.stop(true)
		return c.logReconnectFailure(err)
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
				if !terminalStateSeen {
					c.emitState(session.ctx, ConnectionState{
						Status: ConnectionDisconnected,
						Detail: "Twitch IRC connection closed",
						At:     time.Now(),
					})
				}
				c.scheduleAutoReconnect(c.currentGeneration())
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

// autoReconnect retries the connection with exponential backoff after the
// transport ends on its own.
//
// Chat previously stayed down until someone noticed and pressed ctrl+r. On a
// stream that means dead chat for however long it takes to look at the
// terminal -- and Twitch drops connections routinely, for a server restart or
// a momentary network blip, not only for anything wrong with twi. Backoff
// keeps a persistent outage from turning into a reconnect flood.
func (c *LiveChatClient) autoReconnect(gen uint64) {
	if c.factory == nil {
		return
	}
	delay := initialAutoReconnectDelay
	for attempt := 1; attempt <= maxAutoReconnectAttempts; attempt++ {
		if c.stale(gen) {
			return
		}
		select {
		case <-c.done:
			return
		case <-time.After(delay):
		}
		// Rechecked after the sleep, not only before it: the whole point of
		// the generation check is the window while this goroutine is
		// sleeping, which is exactly when the failure UI tells the user to
		// press ctrl+r. A manual reconnect that lands during the backoff
		// installs a new session, and without this check the goroutine would
		// wake up and tear that healthy session straight back down.
		if c.isClosed() || c.reconnectingNow() || c.stale(gen) {
			return
		}

		ctx, cancel := context.WithTimeout(c.baseCtx, autoReconnectTimeout)
		err := c.reconnect(ctx, "automatic")
		cancel()
		if err == nil {
			c.debugLiveEvent("live_chat.auto_reconnect.succeeded", slog.Int("attempt", attempt))
			return
		}
		if errors.Is(err, ErrLiveChatClientClosed) || errors.Is(err, ErrReconnectUnavailable) {
			return
		}
		c.debugLiveEvent("live_chat.auto_reconnect.failed",
			slog.Int("attempt", attempt),
			slog.String("error", credentialSafeDetail(err)),
		)
		delay = min(delay*2, maxAutoReconnectDelay)
	}
	if c.isClosed() || c.stale(gen) {
		return
	}
	c.emitState(c.baseCtx, ConnectionState{
		Status: ConnectionFailed,
		Detail: "automatic reconnect gave up after repeated failures; retry with ctrl+r",
		At:     time.Now(),
	})
}

// stale reports whether a newer session has been installed since gen, meaning
// whichever goroutine is asking was spawned for a connection that no longer
// exists and has nothing left to do.
func (c *LiveChatClient) stale(gen uint64) bool {
	return c.currentGeneration() != gen
}

func (c *LiveChatClient) isClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closedFlag
}

func (c *LiveChatClient) reconnectingNow() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reconnecting
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
	c.sessionGen++
}

// currentGeneration reports how many sessions have been installed so far.
func (c *LiveChatClient) currentGeneration() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionGen
}

// scheduleAutoReconnect starts an auto-reconnect goroutine bound to gen, the
// session generation that was current when the connection dropped.
//
// The counter is incremented under the same lock that reads closedFlag, and
// Close sets closedFlag before it waits, so no goroutine can be registered
// after Close has started waiting for them.
func (c *LiveChatClient) scheduleAutoReconnect(gen uint64) {
	c.mu.Lock()
	if c.closedFlag {
		c.mu.Unlock()
		return
	}
	c.autoReconnects.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.autoReconnects.Done()
		c.autoReconnect(gen)
	}()
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
		c.emitModeration(ctx, event.Moderation)
	case twitch.EventMembership:
		c.emitMembership(ctx, event.Membership)
	case twitch.EventUserState:
		c.emitUserState(ctx, event.UserState)
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

// emitMessage hands a message to the model, dropping the oldest queued
// message rather than blocking when the buffer is full.
//
// Blocking here stalls the bridge goroutine, which back-pressures into
// go-twitch-irc's single parser goroutine -- the same goroutine that answers
// PING. A UI that falls behind during a raid would therefore stop the client
// responding to keepalives, and Twitch would disconnect it. Every other
// emitter on this type already drops for the same reason; this one was the
// exception.
//
// Dropping the oldest keeps chat current, which is what someone watching a
// busy channel wants: the queue holds messages that have not been displayed
// yet, so nothing already on screen is lost. The count is surfaced in the
// status bar, because a moderator would rightly object to silent drops.
func (c *LiveChatClient) emitMessage(ctx context.Context, msg twitch.ChatMessage) {
	select {
	case c.messages <- msg:
		return
	case <-ctx.Done():
		return
	case <-c.done:
		return
	default:
	}

	// Full: discard the oldest queued message to make room for this one.
	select {
	case <-c.messages:
		c.droppedMessages.Add(1)
	default:
	}
	select {
	case c.messages <- msg:
	case <-ctx.Done():
	case <-c.done:
	default:
		c.droppedMessages.Add(1)
	}
}

// DroppedMessages reports how many chat messages were discarded because the
// UI could not keep up, including any the transport dropped upstream.
func (c *LiveChatClient) DroppedMessages() uint64 {
	dropped := c.droppedMessages.Load()
	if source, ok := c.currentTransportForDrops().(twitch.EventDropCounter); ok {
		dropped += source.DroppedEvents()
	}
	return dropped
}

func (c *LiveChatClient) currentTransportForDrops() twitch.ChatClient {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.session == nil {
		return nil
	}
	return c.session.transport
}

// emitUserState drops the event rather than blocking when the buffer is
// full: USERSTATE repeats after every message the user sends, so the newest
// one always supersedes anything a stalled consumer has not read yet.
func (c *LiveChatClient) emitUserState(ctx context.Context, state twitch.UserState) {
	select {
	case c.userStates <- state:
	case <-ctx.Done():
	case <-c.done:
	default:
	}
}

// emitModeration drops the event rather than blocking when the buffer is
// full, matching the other side-channel emitters. A dropped deletion leaves
// the original message visible, which is the same outcome as twi never having
// been told - not a message that gets reprinted.
func (c *LiveChatClient) emitModeration(ctx context.Context, event twitch.ModerationEvent) {
	select {
	case c.moderations <- event:
	case <-ctx.Done():
	case <-c.done:
	default:
	}
}

// emitMembership drops the event rather than blocking when the buffer is
// full. Busy channels can burst thousands of JOINs after a reconnect, and a
// stalled membership consumer must never wedge the message stream behind it.
func (c *LiveChatClient) emitMembership(ctx context.Context, membership twitch.MembershipEvent) {
	select {
	case c.memberships <- membership:
	case <-ctx.Done():
	case <-c.done:
	default:
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
	systemEventID := userNoticeSystemEventID(notice)
	return twitch.ChatMessage{
		ID:            notice.ID,
		Channel:       notice.Channel,
		ChannelID:     notice.RoomID,
		Timestamp:     notice.Timestamp,
		AuthorLogin:   notice.AuthorLogin,
		AuthorID:      notice.AuthorID,
		DisplayName:   notice.DisplayName,
		AuthorColor:   notice.AuthorColor,
		Badges:        notice.Badges,
		Text:          text,
		Fragments:     notice.Fragments,
		Emotes:        notice.Emotes,
		Type:          twitch.MessageTypeNotice,
		SystemEventID: systemEventID,
		RawTags:       userNoticeRawTags(notice, systemEventID),
	}
}

func userNoticeSystemEventID(notice twitch.UserNotice) string {
	if eventID := strings.TrimSpace(notice.MessageID); eventID != "" {
		return eventID
	}
	return strings.TrimSpace(notice.RawTags["msg-id"])
}

func userNoticeRawTags(notice twitch.UserNotice, systemEventID string) map[string]string {
	tags := cloneAppStringMap(notice.RawTags)
	if systemEventID == "" {
		return tags
	}
	if tags == nil {
		tags = make(map[string]string, 3)
	}
	if _, ok := tags["msg-id"]; !ok {
		tags["msg-id"] = systemEventID
	}
	tags["twi.kind"] = "user_notice"
	tags["twi.event"] = systemEventID
	return tags
}

func cloneAppStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// authNoticeTexts are the NOTICE bodies Twitch sends when login itself fails.
//
// These arrive before registration completes, on the "*" channel, with no
// msg-id, so the text is the only signal available. Every tagged notice --
// msg_banned, no_permission, msg_channel_suspended and the rest -- describes
// channel or account state on a connection that authenticated fine, and none
// of them belong here.
var authNoticeTexts = []string{
	"login authentication failed",
	"login unsuccessful",
	"improperly formatted auth",
}

// isAuthNotice reports whether a NOTICE means the credentials were rejected.
//
// It matches the specific bodies Twitch sends for a failed login rather than
// searching for "auth", "invalid", "permission" or "scope" anywhere in the
// notice. That broad matching flipped the whole client to ConnectionFailed --
// red status bar, "verify your OAuth token" -- on an ordinary no_permission
// notice, while the connection was perfectly healthy.
func isAuthNotice(notice twitch.Notice) bool {
	text := strings.ToLower(strings.TrimSpace(notice.Text))
	if text == "" {
		return false
	}
	return slices.ContainsFunc(authNoticeTexts, func(known string) bool {
		return strings.Contains(text, known)
	})
}

func isTerminalEvent(event twitch.Event) bool {
	if event.Kind == twitch.EventError {
		return true
	}
	return event.Kind == twitch.EventConnection && event.Connection.Type == twitch.ConnectionEventDisconnect
}

// safeError reports a redacted message while keeping the original error
// reachable through errors.Is and errors.As.
//
// Replacing an error with errors.New(redacted) is what this code used to do.
// It kept credentials out of the display, but it also discarded the chain, so
// every errors.Is check downstream stopped matching -- including the
// context.DeadlineExceeded branch in Reconnect, which could not fire because
// the error reaching it had already been flattened to a string.
type safeError struct {
	detail string
	cause  error
}

func (e *safeError) Error() string { return e.detail }

func (e *safeError) Unwrap() error { return e.cause }

func credentialSafeError(err error) error {
	if err == nil {
		return nil
	}
	return &safeError{detail: credentialSafeDetail(err), cause: err}
}

// isRateLimited reports whether the send was refused by twi's own limiter
// rather than by Twitch. SendResult.RateLimited and the composer's
// rate-limited state both existed with nothing setting them, so hitting the
// ceiling was indistinguishable from a successful send.
func isRateLimited(err error) bool {
	return errors.Is(err, twitch.ErrRateLimited) || errors.Is(err, twitch.ErrDuplicateMessage)
}

func credentialSafeSendError(err error) error {
	if err == nil {
		return nil
	}
	return &safeError{detail: credentialSafeSendDetail(err), cause: err}
}

// credentialSafeDetail renders err for display without leaking credentials.
//
// Auth failures are identified by twitch.ErrAuthFailed, which the transport
// attaches when Twitch actually rejected the credentials -- not by searching
// the message for "auth". That search reported "x509: certificate signed by
// unknown authority" as a bad OAuth token, because "authority" contains
// "auth", sending anyone behind a corporate TLS proxy to re-run a login that
// would never have helped. Everything else is shown redacted, as itself.
func credentialSafeDetail(err error) string {
	if err == nil {
		return ""
	}
	if twitch.IsAuthError(err) {
		return "Twitch IRC authentication failed; verify username, OAuth token, and chat:read scope"
	}
	return redactCredentialText(err.Error())
}

func credentialSafeSendDetail(err error) string {
	if err == nil {
		return ""
	}
	if twitch.IsAuthError(err) {
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
