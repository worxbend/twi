package chatservice

import (
	"context"
	"errors"
	"sync"

	"github.com/worxbend/twi/internal/twitch"
)

// Session owns transport cancellation and close. Startup cancellation is separate
// from transport lifetime: ending a readiness timeout must not kill a live session.
// Both terminal and headless clients use this ownership boundary.
type Session struct {
	Context  context.Context
	Events   <-chan twitch.Event
	Reader   Reader
	cancel   context.CancelFunc
	once     sync.Once
	closeErr error
}

// OpenSession adopts reader, including on failure. A failed startup closes it.
func OpenSession(parent, startup context.Context, reader Reader) (*Session, error) {
	if reader == nil {
		return nil, errors.New("missing Twitch chat transport")
	}
	ctx, cancel := context.WithCancel(parent)
	session := &Session{Context: ctx, Reader: reader, cancel: cancel}
	events, err := Connect(startup, ctx, reader)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	session.Events = events
	return session, nil
}

// Close is safe concurrently with startup, reconnect, and repeated shutdown.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() { s.cancel(); s.closeErr = s.Reader.Close() })
	return s.closeErr
}

// Connect bounds startup while giving a transport its full session context.
// It does not adopt reader; OpenSession owns cleanup on failure.
func Connect(startup, lifetime context.Context, reader Reader) (<-chan twitch.Event, error) {
	if err := startup.Err(); err != nil {
		return nil, err
	}
	type result struct {
		events <-chan twitch.Event
		err    error
	}
	done := make(chan result, 1)
	go func() { events, err := reader.Connect(lifetime); done <- result{events, err} }()
	select {
	case <-startup.Done():
		return nil, startup.Err()
	case <-lifetime.Done():
		return nil, lifetime.Err()
	case r := <-done:
		if err := startup.Err(); err != nil {
			return nil, err
		}
		return r.events, r.err
	}
}
