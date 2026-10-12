package chatservice

import (
	"context"
	"errors"
	"github.com/worxbend/twi/internal/twitch"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClient struct {
	events chan twitch.Event
	drops  atomic.Uint64
	closed atomic.Bool
}

func (f *fakeClient) Connect(context.Context) (<-chan twitch.Event, error) { return f.events, nil }
func (f *fakeClient) Send(context.Context, twitch.OutboundMessage) error   { return nil }
func (f *fakeClient) Close() error                                         { f.closed.Store(true); return nil }
func (f *fakeClient) DroppedEvents() uint64                                { return f.drops.Load() }
func receive(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case e := <-events:
		return e
	case <-time.After(time.Second):
		t.Fatal("service event timeout")
		return Event{}
	}
}
func TestReadinessRequiresEveryRoomState(t *testing.T) {
	client := &fakeClient{events: make(chan twitch.Event, 8)}
	service, err := New(Config{Factory: func(context.Context) (Reader, error) { return client, nil }, Channels: []string{"alpha", "beta"}, ConnectTimeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := service.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client.events <- twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventConnect}}
	if e := receive(t, events); e.Kind != EventReceived {
		t.Fatalf("registration marked ready: %+v", e)
	}
	client.events <- twitch.Event{Kind: twitch.EventRoomState, RoomState: twitch.RoomState{Channel: "alpha"}}
	if e := receive(t, events); e.Kind != EventReady || e.Channel != "alpha" {
		t.Fatalf("readiness: %+v", e)
	}
	receive(t, events)
	if e := receive(t, events); e.Kind != EventFailed {
		t.Fatalf("missing beta did not time out: %+v", e)
	}
}
func TestDropsReportedWithoutNextMessage(t *testing.T) {
	client := &fakeClient{events: make(chan twitch.Event, 8)}
	service, _ := New(Config{Factory: func(context.Context) (Reader, error) { return client, nil }, Channels: []string{"alpha"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _ := service.Run(ctx)
	client.drops.Store(7)
	if e := receive(t, events); e.Kind != EventGap || e.Lost != 7 {
		t.Fatalf("loss not reported: %+v", e)
	}
}
func TestHourlyAuthFailureEndsCapture(t *testing.T) {
	client := &fakeClient{events: make(chan twitch.Event, 8)}
	var calls atomic.Int32
	service, _ := New(Config{Factory: func(context.Context) (Reader, error) { return client, nil }, Channels: []string{"alpha"}, ValidationInterval: time.Millisecond, Validate: func(context.Context) error {
		if calls.Add(1) > 1 {
			return errors.New("revoked")
		}
		return nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := service.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if e := receive(t, events); e.Kind != EventGap || e.Reason != "authorization_failed" {
		t.Fatalf("gap: %+v", e)
	}
	if e := receive(t, events); e.Kind != EventFailed {
		t.Fatalf("failure: %+v", e)
	}
}
func TestReconnectCreatesGapAndRechecksQuietReadiness(t *testing.T) {
	first := &fakeClient{events: make(chan twitch.Event, 8)}
	second := &fakeClient{events: make(chan twitch.Event, 8)}
	var calls int
	service, _ := New(Config{Factory: func(context.Context) (Reader, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}, Channels: []string{"alpha"}, ConnectTimeout: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _ := service.Run(ctx)
	first.events <- twitch.Event{Kind: twitch.EventRoomState, RoomState: twitch.RoomState{Channel: "alpha"}}
	receive(t, events)
	receive(t, events)
	first.events <- twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventDisconnect}}
	if e := receive(t, events); e.Kind != EventGap {
		t.Fatalf("reconnect missed gap: %+v", e)
	}
	if e := receive(t, events); e.Kind != EventFailed {
		t.Fatalf("quiet replacement falsely ready: %+v", e)
	}
	if !first.closed.Load() {
		t.Fatal("old transport not closed")
	}
}

func TestPartialReadinessExcludesUnavailableChannel(t *testing.T) {
	client := &fakeClient{events: make(chan twitch.Event, 8)}
	service, _ := New(Config{Factory: func(context.Context) (Reader, error) { return client, nil }, Channels: []string{"alpha", "beta"}, AllowPartial: true, ConnectTimeout: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _ := service.Run(ctx)
	client.events <- twitch.Event{Kind: twitch.EventRoomState, RoomState: twitch.RoomState{Channel: "alpha"}}
	receive(t, events)
	receive(t, events)
	if e := receive(t, events); e.Kind != EventUnavailable || e.Channel != "beta" {
		t.Fatalf("missing exclusion: %+v", e)
	}
	client.events <- twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{Channel: "alpha", Text: "still live"}}
	if e := receive(t, events); e.Kind != EventReceived || e.Twitch.Message.Text != "still live" {
		t.Fatalf("healthy channel stopped: %+v", e)
	}
}

type partiallyResolvedReader struct {
	*fakeClient
	unavailable []string
}

func (p *partiallyResolvedReader) UnavailableChannels() []string { return p.unavailable }
func TestIdentityExclusionDoesNotWaitForReadinessTimeout(t *testing.T) {
	client := &partiallyResolvedReader{fakeClient: &fakeClient{events: make(chan twitch.Event, 8)}, unavailable: []string{"missing"}}
	service, _ := New(Config{Factory: func(context.Context) (Reader, error) { return client, nil }, Channels: []string{"alpha", "missing"}, AllowPartial: true, ConnectTimeout: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := service.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if e := receive(t, events); e.Kind != EventUnavailable || e.Channel != "missing" {
		t.Fatalf("exclusion=%+v", e)
	}
	client.events <- twitch.Event{Kind: twitch.EventChannelReady, RoomState: twitch.RoomState{Channel: "alpha"}}
	if e := receive(t, events); e.Kind != EventReady {
		t.Fatalf("healthy channel=%+v", e)
	}
	receive(t, events)
	time.Sleep(15 * time.Millisecond)
	client.events <- twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{Channel: "alpha", Text: "ready after deadline"}}
	if e := receive(t, events); e.Kind != EventReceived || e.Twitch.Message.Text != "ready after deadline" {
		t.Fatalf("excluded identity blocked readiness=%+v", e)
	}
}

func TestStartupAuthRetriesPreserveInitialReadinessDeadline(t *testing.T) {
	client := &fakeClient{events: make(chan twitch.Event, 8)}
	service, _ := New(Config{Factory: func(context.Context) (Reader, error) { return client, nil }, Channels: []string{"alpha"}, ConnectTimeout: 20 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	events, err := service.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case client.events <- twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventReconnect, Reason: "refreshing access token"}}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	for {
		select {
		case e := <-events:
			if e.Kind == EventGap {
				t.Fatalf("startup refresh became capture gap: %+v", e)
			}
			if e.Kind == EventFailed {
				return
			}
		case <-ctx.Done():
			t.Fatal("startup retries renewed initial timeout")
		}
	}
}
func TestReconnectAfterObservedRecordRequiresGapBeforeReadiness(t *testing.T) {
	client := &fakeClient{events: make(chan twitch.Event, 8)}
	service, _ := New(Config{Factory: func(context.Context) (Reader, error) { return client, nil }, Channels: []string{"alpha", "beta"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _ := service.Run(ctx)
	client.events <- twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{Channel: "alpha", Text: "observed"}}
	if e := receive(t, events); e.Kind != EventReceived {
		t.Fatalf("message=%+v", e)
	}
	client.events <- twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventReconnect}}
	if e := receive(t, events); e.Kind != EventGap {
		t.Fatalf("observed prefix lost gap: %+v", e)
	}
}

func TestEmptySelectionReconnectHasNoChannelReadinessDeadline(t *testing.T) {
	first := &fakeClient{events: make(chan twitch.Event, 4)}
	replacement := &fakeClient{events: make(chan twitch.Event, 4)}
	calls := 0
	service, err := New(Config{AllowEmptySelection: true, ConnectTimeout: 10 * time.Millisecond, Factory: func(context.Context) (Reader, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return replacement, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := service.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first.events <- twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventDisconnect}}
	// A replacement connection is confirmed without joining any channel.
	replacement.events <- twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventConnect}}
	replacement.events <- twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventReconnect}}
	confirmed := false
	for !confirmed {
		select {
		case event := <-events:
			if event.Kind == EventFailed {
				t.Fatalf("empty reconnect failed: %v", event.Err)
			}
			if event.Kind == EventReceived && event.Twitch.Kind == twitch.EventConnection && event.Twitch.Connection.Type == twitch.ConnectionEventReconnect {
				confirmed = true
			}
		case <-time.After(time.Second):
			t.Fatal("empty reconnect did not complete")
		}
	}
	select {
	case event := <-events:
		if event.Kind == EventFailed {
			t.Fatalf("invented empty-channel readiness deadline: %v", event.Err)
		}
	case <-time.After(50 * time.Millisecond):
	}
}
