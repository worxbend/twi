package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/twitch"
)

func TestSharedIRCServiceReconnectReplaysRuntimeChannels(t *testing.T) {
	t.Parallel()
	first, second := newFakeTwitchTransport(16), newFakeTwitchTransport(16)
	count := 0
	var mu sync.Mutex
	factory := func(context.Context) (twitch.ChatClient, error) {
		mu.Lock()
		defer mu.Unlock()
		count++
		if count == 1 {
			return first, nil
		}
		return second, nil
	}
	client, err := NewServiceIRCClient(context.Background(), factory, []string{"alpha"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	first.emit(twitch.Event{Kind: twitch.EventRoomState, RoomState: twitch.RoomState{Channel: "alpha"}})
	first.emit(twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{ID: "before", Channel: "alpha", Text: "observed"}})
	select {
	case message := <-client.Messages():
		if message.ID != "before" {
			t.Fatal(message)
		}
	case <-time.After(time.Second):
		t.Fatal("shared service did not receive")
	}
	if err := client.JoinChannel(context.Background(), "#Beta"); err != nil {
		t.Fatal(err)
	}
	if err := client.PartChannel(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := client.Reconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !first.closedCalled() {
		t.Fatal("replacement started before prior transport closed")
	}
	if got := second.joinedChannels(); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("joins=%v", got)
	}
	if got := second.departedChannels(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("parts=%v", got)
	}
	if result, err := client.Send(context.Background(), SendRequest{Channel: "beta", Text: "hello", Action: true}); err != nil || !result.AcceptedAt.IsZero() || result.QueuedAt.IsZero() {
		t.Fatalf("IRC result=%+v err=%v", result, err)
	}
}
func TestSharedServiceTerminalFailureEndsRecordingCoverage(t *testing.T) {
	t.Parallel()
	reader := newFakeTwitchTransport(8)
	recorder := &nativeRecorder{records: make(chan automation.Record, 8), ready: make(chan string, 8), lost: make(chan struct{}, 8)}
	source, err := NewServiceChatSource(context.Background(), func(context.Context) (*chatservice.Service, error) {
		return chatservice.New(chatservice.Config{Channels: []string{"alpha"}, Factory: func(context.Context) (chatservice.Reader, error) { return reader, nil }})
	}, "eventsub", recorder)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	reader.emit(twitch.Event{Kind: twitch.EventChannelReady, RoomState: twitch.RoomState{Channel: "alpha"}})
	select {
	case <-recorder.ready:
	case <-time.After(time.Second):
		t.Fatal("readiness not recorded")
	}
	reader.emit(twitch.Event{Kind: twitch.EventError, Err: twitch.ErrAuthFailed})
	select {
	case <-recorder.lost:
	case <-time.After(time.Second):
		t.Fatal("terminal failure left coverage open")
	}
}

func TestSharedIRCServiceStartsWithChannelPicker(t *testing.T) {
	t.Parallel()
	reader := newFakeTwitchTransport(8)
	client, err := NewServiceIRCClient(context.Background(), func(context.Context) (twitch.ChatClient, error) { return reader, nil }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.JoinChannel(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	reader.emit(twitch.Event{Kind: twitch.EventRoomState, RoomState: twitch.RoomState{Channel: "alpha"}})
	reader.emit(twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{ID: "opened", Channel: "alpha", Text: "joined from picker"}})
	select {
	case message := <-client.Messages():
		if message.ID != "opened" {
			t.Fatal(message)
		}
	case <-time.After(time.Second):
		t.Fatal("picker join did not receive")
	}
}

func TestSharedSourceCloseCancelsBlockedManualReconnect(t *testing.T) {
	t.Parallel()
	initial := newFakeTwitchTransport(4)
	blocked := newBlockingConnectTransport()
	calls := 0
	var mu sync.Mutex
	factory := func(context.Context) (twitch.ChatClient, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return initial, nil
		}
		return blocked, nil
	}
	source, err := NewServiceIRCClient(context.Background(), factory, []string{"alpha"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reconnected := make(chan error, 1)
	go func() { reconnected <- source.Reconnect(context.Background()) }()
	select {
	case <-blocked.connectEntered:
	case <-time.After(time.Second):
		t.Fatal("replacement did not reach startup")
	}
	closed := make(chan error, 1)
	go func() { closed <- source.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited for reconnect startup deadline")
	}
	select {
	case err := <-reconnected:
		if err == nil {
			t.Fatal("cancelled reconnect succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect did not observe close")
	}
	if !blocked.closedCalled() {
		t.Fatal("close leaked replacement transport")
	}
}

func TestServiceCriticalStateSurvivesSaturatedQueue(t *testing.T) {
	t.Parallel()
	source := &ServiceChatSource{parent: context.Background(), states: make(chan ConnectionState, 128)}
	for range cap(source.states) {
		source.emitState(context.Background(), ConnectionState{Status: ConnectionConnected, Detail: "old readiness"})
	}
	// A failed manual request has a canceled context, while the UI remains alive.
	request, cancel := context.WithCancel(context.Background())
	cancel()
	source.emitState(request, ConnectionState{Status: ConnectionFailed, Detail: "terminal capability failure"})
	found := false
	for range cap(source.states) {
		state := <-source.states
		if state.Status == ConnectionFailed && state.Detail == "terminal capability failure" {
			found = true
		}
	}
	if !found {
		t.Fatal("terminal failure vanished behind readiness backlog")
	}
}
func TestServiceReadinessStatesAreNotDuplicated(t *testing.T) {
	t.Parallel()
	source := &ServiceChatSource{parent: context.Background(), states: make(chan ConnectionState, 128), readyChannels: map[string]bool{}}
	for i := range 100 {
		channel := fmt.Sprintf("channel_%d", i)
		source.handle(context.Background(), chatservice.Event{Kind: chatservice.EventReady, Channel: channel})
		source.handle(context.Background(), chatservice.Event{Kind: chatservice.EventReceived, Twitch: twitch.Event{Kind: twitch.EventRoomState, RoomState: twitch.RoomState{Channel: channel}}})
	}
	if len(source.states) != 100 {
		t.Fatalf("readiness queue=%d want100", len(source.states))
	}
}
