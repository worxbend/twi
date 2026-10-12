package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/output"
	"github.com/worxbend/twi/internal/twitch"
)

// ServiceChatSource adapts the common live service into terminal channels.
// Automatic readiness/retries/gaps stay in chatservice; manual reconnect replaces
// the entire service generation after stopping its predecessor.
type ServiceChatSource struct {
	factory         func(context.Context) (*chatservice.Service, error)
	parent          context.Context
	lifetimeCancel  context.CancelFunc
	recorder        ChatRecorder
	messages        chan twitch.ChatMessage
	states          chan ConnectionState
	moderations     chan twitch.ModerationEvent
	users           chan twitch.UserState
	memberships     chan twitch.MembershipEvent
	dropped         atomic.Uint64
	lifecycle       sync.Mutex
	mu              sync.Mutex
	cancel          context.CancelFunc
	stopped         chan struct{}
	closed          bool
	once            sync.Once
	transport       string
	identity        twitch.TokenIdentity
	captureChannels map[string]bool
	disabledCapture map[string]bool
	readyChannels   map[string]bool
}

func NewServiceChatSource(ctx context.Context, factory func(context.Context) (*chatservice.Service, error), transport string, recorder ChatRecorder) (*ServiceChatSource, error) {
	lifetime, lifetimeCancel := context.WithCancel(ctx)
	c := &ServiceChatSource{lifetimeCancel: lifetimeCancel, factory: factory, parent: lifetime, recorder: recorder, transport: transport, messages: make(chan twitch.ChatMessage, 128), states: make(chan ConnectionState, 128), moderations: make(chan twitch.ModerationEvent, 128), users: make(chan twitch.UserState, 128), memberships: make(chan twitch.MembershipEvent, 128), disabledCapture: map[string]bool{}}
	if factory == nil {
		lifetimeCancel()
		return nil, errors.New("live service factory required")
	}
	if err := c.start(ctx); err != nil {
		lifetimeCancel()
		return nil, err
	}
	return c, nil
}
func (c *ServiceChatSource) Messages() <-chan twitch.ChatMessage        { return c.messages }
func (c *ServiceChatSource) ConnectionStates() <-chan ConnectionState   { return c.states }
func (c *ServiceChatSource) Moderations() <-chan twitch.ModerationEvent { return c.moderations }
func (c *ServiceChatSource) UserStates() <-chan twitch.UserState        { return c.users }
func (c *ServiceChatSource) Memberships() <-chan twitch.MembershipEvent { return c.memberships }
func (c *ServiceChatSource) DroppedMessages() uint64                    { return c.dropped.Load() }
func (c *ServiceChatSource) emitState(ctx context.Context, state ConnectionState) {
	if criticalConnectionState(state.Status) && c.parent != nil {
		ctx = c.parent
	}
	emitConnectionState(ctx, c.states, state)
}

func criticalConnectionState(status ConnectionStatus) bool {
	switch status {
	case ConnectionFailed, ConnectionClosed, ConnectionDisconnected, ConnectionReconnecting:
		return true
	}
	return false
}

// Terminal/health failures cannot disappear behind stale connected states.
// These streams are buffered and their owner closes them after emitters stop.
func emitConnectionState(ctx context.Context, states chan ConnectionState, state ConnectionState) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case states <- state:
			return
		default:
		}
		if !criticalConnectionState(state.Status) || cap(states) == 0 {
			return
		}
		select {
		case <-states:
		default:
		}
	}
}

func (c *ServiceChatSource) start(startup context.Context) error {
	run, cancel := context.WithCancel(c.parent)
	bounded, cancelBounded := context.WithCancel(startup)
	stopLifetime := context.AfterFunc(run, cancelBounded)
	defer stopLifetime()
	defer cancelBounded()
	service, err := c.factory(bounded)
	if err != nil {
		cancel()
		return err
	}
	if err := startup.Err(); err != nil {
		cancel()
		return err
	}
	stop := context.AfterFunc(bounded, cancel)
	events, err := service.Run(run)
	stop()
	if bounded.Err() != nil {
		cancel()
		if err == nil {
			for range events {
			}
		}
		return bounded.Err()
	}
	if err != nil {
		cancel()
		return err
	}
	selected := map[string]bool{}
	for _, channel := range service.SelectedChannels() {
		selected[twitch.ChannelKey(channel)] = true
	}
	done := make(chan struct{})
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		for range events {
		}
		return ErrLiveChatClientClosed
	}
	c.captureChannels = selected
	c.readyChannels = map[string]bool{}
	c.identity = service.Identity()
	c.cancel = cancel
	c.stopped = done
	c.mu.Unlock()
	c.emitState(run, ConnectionState{Status: ConnectionConnecting, Detail: "connecting to Twitch " + c.transport, At: time.Now()})
	go func() {
		defer close(done)
		defer func() {
			if c.recorder != nil {
				end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := c.recorder.Lost(end, time.Now().UTC()); err != nil {
					c.captureFailure(err)
				}
			}
		}()
		for {
			select {
			case <-run.Done():
				for range events {
				}
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				c.handle(run, event)
			}
		}
	}()
	return nil
}
func (c *ServiceChatSource) Reconnect(ctx context.Context) error {
	if !c.lifecycle.TryLock() {
		return ErrReconnectInProgress
	}
	defer c.lifecycle.Unlock()
	c.mu.Lock()
	closed, cancel, done := c.closed, c.cancel, c.stopped
	c.mu.Unlock()
	if closed {
		return ErrLiveChatClientClosed
	}
	cancel()
	<-done
	if c.recorder != nil {
		if err := c.recorder.Lost(ctx, time.Now().UTC()); err != nil {
			c.captureFailure(err)
		}
	}
	c.emitState(ctx, ConnectionState{Status: ConnectionReconnecting, Detail: "restarting Twitch " + c.transport, At: time.Now()})
	if err := c.start(ctx); err != nil {
		c.emitState(ctx, ConnectionState{Status: ConnectionFailed, Detail: credentialSafeDetail(err), Err: err, At: time.Now()})
		return err
	}
	return nil
}
func (c *ServiceChatSource) Close() error {
	c.once.Do(func() {
		// Cancel before taking the lifecycle lock: an in-flight reconnect may be
		// blocked in transport startup while holding that lock.
		c.mu.Lock()
		c.closed = true
		c.lifetimeCancel()
		if c.cancel != nil {
			c.cancel()
		}
		c.mu.Unlock()
		c.lifecycle.Lock()
		defer c.lifecycle.Unlock()
		c.mu.Lock()
		c.closed = true
		cancel, done := c.cancel, c.stopped
		c.mu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
		close(c.messages)
		close(c.states)
		close(c.moderations)
		close(c.users)
		close(c.memberships)
	})
	return nil
}
func (c *ServiceChatSource) captureFailure(err error) {
	if sink, ok := c.recorder.(interface{ Fail(error) }); ok {
		sink.Fail(err)
	}
}
func (c *ServiceChatSource) handle(ctx context.Context, event chatservice.Event) {
	switch event.Kind {
	case chatservice.EventReady:
		c.mu.Lock()
		c.readyChannels[twitch.ChannelKey(event.Channel)] = true
		c.mu.Unlock()
		if c.recorder != nil && c.captureSelected(event.Channel) {
			if err := c.recorder.Ready(ctx, event.Channel, event.At); err != nil {
				c.captureFailure(err)
			}
		}
		c.emitState(ctx, ConnectionState{Status: ConnectionConnected, Channel: event.Channel, Detail: "joined Twitch " + c.transport, At: event.At})
	case chatservice.EventGap:
		if event.Reason != "upstream_overflow" {
			c.mu.Lock()
			clear(c.readyChannels)
			c.mu.Unlock()
		}
		c.dropped.Add(event.Lost)
		if c.recorder != nil {
			if event.Reason == "upstream_overflow" {
				if sink, ok := c.recorder.(interface {
					Loss(context.Context, time.Time, time.Time, uint64) error
				}); ok {
					if err := sink.Loss(ctx, event.Since, event.At, event.Lost); err != nil {
						c.captureFailure(err)
					}
				}
			} else {
				if err := c.recorder.Lost(ctx, event.At); err != nil {
					c.captureFailure(err)
				}
			}
		}
		status := ConnectionReconnecting
		if event.Reason == "upstream_overflow" {
			status = ConnectionConnected
		}
		c.emitState(ctx, ConnectionState{Status: status, Detail: "capture gap: " + event.Reason, At: event.At})
	case chatservice.EventFailed:
		c.emitState(ctx, ConnectionState{Status: ConnectionFailed, Detail: credentialSafeDetail(event.Err), Err: event.Err, At: event.At})
	case chatservice.EventUnavailable:
		c.emitState(ctx, ConnectionState{Status: ConnectionFailed, Channel: event.Channel, Detail: event.Reason, At: event.At})
	case chatservice.EventReceived:
		e := event.Twitch
		if c.recorder != nil {
			if record, ok := output.FromEvent(e, event.At, c.transport); ok && c.captureSelected(record.Channel.Login) {
				if err := c.recorder.Submit(record); err != nil {
					c.captureFailure(err)
				}
			}
		}
		var message *twitch.ChatMessage
		switch e.Kind {
		case twitch.EventConnection:
			c.emitState(ctx, stateFromConnectionEvent(e.Connection))
		case twitch.EventRoomState, twitch.EventChannelReady:
			if c.recorder != nil && c.captureSelected(e.RoomState.Channel) {
				if err := c.recorder.Ready(ctx, twitch.ChannelKey(e.RoomState.Channel), event.At); err != nil {
					c.captureFailure(err)
				}
			}
			channel := twitch.ChannelKey(e.RoomState.Channel)
			c.mu.Lock()
			alreadyReady := c.readyChannels[channel]
			c.readyChannels[channel] = true
			c.mu.Unlock()
			if !alreadyReady {
				c.emitState(ctx, ConnectionState{Status: ConnectionConnected, Channel: channel, Detail: "joined Twitch " + c.transport, At: event.At})
			}
		case twitch.EventMessage:
			message = &e.Message
		case twitch.EventNotice:
			m := messageFromNotice(e.Notice)
			message = &m
		case twitch.EventUserNotice:
			m := messageFromUserNotice(e.UserNotice)
			message = &m
		case twitch.EventModeration:
			select {
			case c.moderations <- e.Moderation:
			case <-ctx.Done():
			}
		case twitch.EventUserState:
			select {
			case c.users <- e.UserState:
			case <-ctx.Done():
			default:
			}
		case twitch.EventMembership:
			select {
			case c.memberships <- e.Membership:
			case <-ctx.Done():
			default:
			}
		}
		if message != nil {
			select {
			case c.messages <- *message:
			case <-ctx.Done():
			default:
				c.dropped.Add(1)
			}
		}
	}
}

func (c *ServiceChatSource) captureSelected(channel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := twitch.ChannelKey(channel)
	return c.captureChannels[key] && !c.disabledCapture[key]
}
func (c *ServiceChatSource) setCaptureSelected(ctx context.Context, channel string, enabled bool) {
	c.mu.Lock()
	key := twitch.ChannelKey(channel)
	c.disabledCapture[key] = !enabled
	if !enabled {
		delete(c.readyChannels, key)
	}
	selected := c.captureChannels[key]
	c.mu.Unlock()
	if !enabled && selected && c.recorder != nil {
		if sink, ok := c.recorder.(interface {
			ChannelLost(context.Context, string, time.Time) error
		}); ok {
			if err := sink.ChannelLost(ctx, key, time.Now().UTC()); err != nil {
				c.captureFailure(err)
			}
		} else {
			if err := c.recorder.Lost(ctx, time.Now().UTC()); err != nil {
				c.captureFailure(err)
			}
		}
	}
}

func (c *ServiceChatSource) Identity() twitch.TokenIdentity {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.identity
}
