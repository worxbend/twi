// Package chatservice owns a UI-independent live reader and capture health.
package chatservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

type Kind string

const (
	EventReceived    Kind = "event"
	EventReady       Kind = "ready"
	EventGap         Kind = "gap"
	EventFailed      Kind = "error"
	EventUnavailable Kind = "unavailable"
)

type Event struct {
	Kind    Kind
	Twitch  twitch.Event
	Channel string
	At      time.Time
	Since   time.Time
	Reason  string
	Lost    uint64
	Err     error
}

// Reader receives normalized events without any send capability.
type Reader interface {
	Connect(context.Context) (<-chan twitch.Event, error)
	Close() error
}

type Config struct {
	Identity func() twitch.TokenIdentity
	// ReaderChanged exposes optional outbound capabilities to the TUI adapter.
	// The service remains the sole transport owner; nil marks teardown.
	ReaderChanged      func(Reader)
	Factory            func(context.Context) (Reader, error)
	Channels           []string
	ConnectTimeout     time.Duration
	Buffer             int
	Validate           func(context.Context) error
	ValidationInterval time.Duration
	MaxReconnects      int
	AllowPartial       bool
	// AllowEmptySelection permits the TUI channel picker to start before joining.
	AllowEmptySelection bool
}
type Service struct{ config Config }

func New(config Config) (*Service, error) {
	if config.Factory == nil {
		return nil, errors.New("chat reader factory required")
	}
	selected, err := twitch.ParseChannels(config.Channels)
	if err != nil {
		return nil, err
	}
	config.Channels = make([]string, len(selected))
	for i, channel := range selected {
		config.Channels[i] = channel.String()
	}
	if len(config.Channels) == 0 && !config.AllowEmptySelection || len(config.Channels) > 100 {
		return nil, errors.New("chat reader requires 1 to 100 channels")
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 30 * time.Second
	}
	if config.ValidationInterval <= 0 {
		config.ValidationInterval = time.Hour
	}
	if config.Buffer <= 0 {
		config.Buffer = 128
	}
	if config.MaxReconnects <= 0 {
		config.MaxReconnects = 5
	}
	config.Channels = append([]string(nil), config.Channels...)
	return &Service{config: config}, nil
}

// SelectedChannels returns a copy of the capture selection.
func (s *Service) Identity() twitch.TokenIdentity {
	if s.config.Identity != nil {
		return s.config.Identity()
	}
	return twitch.TokenIdentity{}
}

func (s *Service) SelectedChannels() []string { return append([]string(nil), s.config.Channels...) }

// Run starts a bounded reader. Callers must cancel the context when done.
func (s *Service) Run(ctx context.Context) (<-chan Event, error) {
	startup, cancel := context.WithTimeout(ctx, s.config.ConnectTimeout)
	defer cancel()
	if s.config.Validate != nil {
		if err := s.config.Validate(startup); err != nil {
			return nil, err
		}
	}
	client, err := s.config.Factory(startup)
	if err != nil {
		return nil, err
	}
	session, err := OpenSession(ctx, startup, client)
	if err != nil {
		return nil, fmt.Errorf("chat startup: %w", err)
	}
	upstream := session.Events
	remaining := s.config.ConnectTimeout
	if limit, ok := startup.Deadline(); ok {
		remaining = time.Until(limit)
	}
	if remaining <= 0 {
		_ = session.Close()
		return nil, errors.New("chat startup timed out")
	}
	output := make(chan Event, s.config.Buffer)
	if s.config.ReaderChanged != nil {
		s.config.ReaderChanged(client)
	}
	go s.run(ctx, session, upstream, output, remaining)
	return output, nil
}
func (s *Service) run(ctx context.Context, session *Session, upstream <-chan twitch.Event, output chan Event, initialTimeout time.Duration) {
	defer close(output)
	defer func() {
		if s.config.ReaderChanged != nil {
			s.config.ReaderChanged(nil)
		}
	}()
	defer func() { _ = session.Close() }()
	client := session.Reader
	emit := func(e Event) bool {
		if e.At.IsZero() {
			e.At = time.Now().UTC()
		}
		select {
		case output <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	ready := make(map[string]bool)
	excluded := make(map[string]bool)
	reportUnavailable := func() bool {
		if selection, ok := client.(interface{ UnavailableChannels() []string }); ok {
			for _, channel := range selection.UnavailableChannels() {
				channel = twitch.ChannelKey(channel)
				if !excluded[channel] {
					excluded[channel] = true
					if !emit(Event{Kind: EventUnavailable, Channel: channel, Reason: "channel identity unavailable"}) {
						return false
					}
				}
			}
		}
		return true
	}
	if s.config.AllowPartial && !reportUnavailable() {
		return
	}
	initialDeadline := time.Now().Add(initialTimeout)
	initialComplete, observed := len(s.config.Channels) == 0, false
	deadline := time.NewTimer(initialTimeout)
	if initialComplete {
		deadline.Stop()
	}
	defer deadline.Stop()
	validation := time.NewTicker(s.config.ValidationInterval)
	defer validation.Stop()
	dropped := time.NewTicker(100 * time.Millisecond)
	defer dropped.Stop()
	var previous uint64
	lastLossCheck := time.Now().UTC()
	reportLoss := func() bool {
		if counter, ok := client.(twitch.EventDropCounter); ok {
			n := counter.DroppedEvents()
			checkedAt := time.Now().UTC()
			since := lastLossCheck
			lastLossCheck = checkedAt
			if n > previous {
				loss := n - previous
				previous = n
				return emit(Event{Kind: EventGap, Reason: "upstream_overflow", Lost: loss, At: checkedAt, Since: since})
			}
		}
		return true
	}
	retries := 0
	reconnect := func() bool {
		_ = session.Close()
		if s.config.ReaderChanged != nil {
			s.config.ReaderChanged(nil)
		}
		for retries < s.config.MaxReconnects {
			retries++
			delay := ReconnectDelay(retries, 250*time.Millisecond, 16*time.Second)
			if !initialComplete {
				remaining := time.Until(initialDeadline)
				if remaining <= 0 {
					emit(Event{Kind: EventFailed, Err: errors.New("chat startup readiness timed out")})
					return false
				}
				delay = min(delay, remaining)
			}
			wait := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				wait.Stop()
				return false
			case <-wait.C:
			}
			timeout := s.config.ConnectTimeout
			if !initialComplete {
				timeout = min(timeout, time.Until(initialDeadline))
				if timeout <= 0 {
					emit(Event{Kind: EventFailed, Err: errors.New("chat startup readiness timed out")})
					return false
				}
			}
			bounded, cancel := context.WithTimeout(ctx, timeout)
			replacement, err := s.config.Factory(bounded)
			var next *Session
			if err == nil {
				next, err = OpenSession(ctx, bounded, replacement)
			}
			cancel()
			if err != nil {

				if errors.Is(err, twitch.ErrAuthFailed) {
					emit(Event{Kind: EventFailed, Err: err})
					return false
				}
				continue
			}
			client = replacement
			session = next
			if s.config.ReaderChanged != nil {
				s.config.ReaderChanged(replacement)
			}
			upstream = next.Events
			previous = 0
			lastLossCheck = time.Now().UTC()
			clear(ready)
			if s.config.AllowPartial && !reportUnavailable() {
				return false
			}
			if len(s.config.Channels) == 0 {
				deadline.Stop()
			} else if initialComplete {
				deadline.Reset(s.config.ConnectTimeout)
			} else {
				deadline.Reset(max(time.Until(initialDeadline), time.Nanosecond))
			}
			return true
		}
		emit(Event{Kind: EventFailed, Err: errors.New("chat reconnect attempts exhausted")})
		return false
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			if s.config.AllowPartial && len(ready) > 0 {
				for _, channel := range s.config.Channels {
					if !ready[twitch.ChannelKey(channel)] && !excluded[twitch.ChannelKey(channel)] {
						excluded[twitch.ChannelKey(channel)] = true
						if !emit(Event{Kind: EventUnavailable, Channel: twitch.ChannelKey(channel), Reason: "channel readiness timed out"}) {
							return
						}
					}
				}
				initialComplete = true
				continue
			}
			emit(Event{Kind: EventFailed, Err: fmt.Errorf("chat channel readiness timed out after %s", s.config.ConnectTimeout)})
			return
		case <-validation.C:
			if s.config.Validate != nil {
				bounded, cancel := context.WithTimeout(ctx, s.config.ConnectTimeout)
				err := s.config.Validate(bounded)
				cancel()
				if err != nil {
					emit(Event{Kind: EventGap, Reason: "authorization_failed", Err: err})
					emit(Event{Kind: EventFailed, Err: err})
					return
				}
			}
		case <-dropped.C:
			if !reportLoss() {
				return
			}
		case e, ok := <-upstream:
			if !reportLoss() {
				return
			}
			if !ok {
				if terminal, ok := client.(interface{ TerminalError() error }); ok {
					if err := terminal.TerminalError(); err != nil {
						emit(Event{Kind: EventFailed, Err: err})
						return
					}
				}
				if observed && !emit(Event{Kind: EventGap, Reason: "disconnected"}) {
					return
				}
				if !reconnect() {
					return
				}
				continue
			}
			if e.Kind == twitch.EventError || (e.Kind == twitch.EventNotice && e.Notice.AuthFailed) {
				err := e.Err
				if err == nil {
					err = twitch.ErrAuthFailed
				}
				emit(Event{Kind: EventFailed, Err: err})
				return
			}
			if e.Kind == twitch.EventConnection {
				switch e.Connection.Type {
				case twitch.ConnectionEventConnect:
					if len(s.config.Channels) == 0 {
						retries = 0
						deadline.Stop()
					}
				case twitch.ConnectionEventDisconnect:
					if errors.Is(e.Connection.Err, twitch.ErrAuthFailed) {
						emit(Event{Kind: EventFailed, Err: e.Connection.Err})
						return
					}
					if observed && !emit(Event{Kind: EventGap, Reason: "disconnected"}) {
						return
					}
					if !reconnect() {
						return
					}
					continue
				case twitch.ConnectionEventReconnect:
					clear(ready)
					if initialComplete && len(s.config.Channels) > 0 {
						deadline.Reset(s.config.ConnectTimeout)
					}
					if observed && !emit(Event{Kind: EventGap, Reason: "reconnecting", At: e.Connection.At}) {
						return
					}
				}
			}
			if e.Kind == twitch.EventRoomState || e.Kind == twitch.EventChannelReady {
				channel := twitch.ChannelKey(e.RoomState.Channel)
				for _, wanted := range s.config.Channels {
					if twitch.ChannelKey(wanted) == channel && !ready[channel] && !excluded[channel] {
						ready[channel] = true
						observed = true
						if !emit(Event{Kind: EventReady, Channel: channel}) {
							return
						}
					}
				}
				if len(ready)+len(excluded) == len(s.config.Channels) {
					initialComplete = true
					retries = 0
					deadline.Stop()
				}
			}
			if e.Kind == twitch.EventNotice {
				for _, channel := range s.config.Channels {
					if twitch.ChannelKey(e.Notice.Channel) == twitch.ChannelKey(channel) {
						observed = true
						break
					}
				}
			}
			if e.Kind == twitch.EventMessage || e.Kind == twitch.EventUserNotice || e.Kind == twitch.EventModeration {
				observed = true
			}
			if !emit(Event{Kind: EventReceived, Twitch: e}) {
				return
			}
		}
	}
}
