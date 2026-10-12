package chatservice

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/worxbend/twi/internal/twitch"
)

type sessionReader struct {
	entered chan context.Context
	closed  atomic.Int32
	block   bool
}

func (r *sessionReader) Connect(ctx context.Context) (<-chan twitch.Event, error) {
	r.entered <- ctx
	if r.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return make(chan twitch.Event), nil
}
func (r *sessionReader) Close() error { r.closed.Add(1); return nil }

func TestSessionSeparatesStartupFromLifetime(t *testing.T) {
	t.Parallel()
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	startup, cancelStartup := context.WithCancel(parent)
	reader := &sessionReader{entered: make(chan context.Context, 1)}
	session, err := OpenSession(parent, startup, reader)
	if err != nil {
		t.Fatal(err)
	}
	lifetime := <-reader.entered
	cancelStartup()
	if lifetime.Err() != nil {
		t.Fatal("startup deadline cancelled live transport")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if reader.closed.Load() != 1 {
		t.Fatalf("transport closed %d times", reader.closed.Load())
	}
	if !errors.Is(lifetime.Err(), context.Canceled) {
		t.Fatal("close did not cancel transport")
	}
}

func TestSessionCancelledStartupClosesAndCancels(t *testing.T) {
	t.Parallel()
	startup, cancel := context.WithCancel(context.Background())
	reader := &sessionReader{entered: make(chan context.Context, 1), block: true}
	result := make(chan error, 1)
	go func() { _, err := OpenSession(context.Background(), startup, reader); result <- err }()
	lifetime := <-reader.entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if reader.closed.Load() != 1 {
		t.Fatal("failed startup leaked transport")
	}
	if !errors.Is(lifetime.Err(), context.Canceled) {
		t.Fatal("failed startup leaked context")
	}
}
