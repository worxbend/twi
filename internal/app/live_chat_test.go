package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/twitch"
)

func TestLiveChatClientBridgesTransportEvents(t *testing.T) {
	transport := newFakeTwitchTransport(8)
	client, err := NewLiveChatClient(context.Background(), transport, 8)
	if err != nil {
		t.Fatalf("NewLiveChatClient returned error: %v", err)
	}
	defer client.Close()

	if !transport.connectCalled() {
		t.Fatal("transport Connect was not called")
	}
	if got := <-client.ConnectionStates(); got.Status != ConnectionConnecting {
		t.Fatalf("initial state = %#v, want connecting", got)
	}

	connectedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	transport.emit(twitch.Event{
		Kind: twitch.EventConnection,
		Connection: twitch.ConnectionEvent{
			Type: twitch.ConnectionEventConnect,
			At:   connectedAt,
		},
	})
	if got := <-client.ConnectionStates(); got.Status != ConnectionConnected || got.Detail == "" {
		t.Fatalf("connected state = %#v, want connected with detail", got)
	}

	msg := twitch.ChatMessage{
		ID:          "live-1",
		Channel:     "example",
		Timestamp:   connectedAt.Add(time.Second),
		AuthorLogin: "viewer",
		DisplayName: "viewer",
		Text:        "hello from IRC",
		Type:        twitch.MessageTypeChat,
	}
	transport.emit(twitch.Event{Kind: twitch.EventMessage, Message: msg})
	if got := <-client.Messages(); !reflect.DeepEqual(got, msg) {
		t.Fatalf("message = %#v, want %#v", got, msg)
	}

	transport.emit(twitch.Event{
		Kind: twitch.EventNotice,
		Notice: twitch.Notice{
			Channel: "example",
			ID:      "msg_banned",
			Text:    "You are banned from talking in this channel.",
		},
	})
	if got := <-client.Messages(); got.Type != twitch.MessageTypeNotice || !strings.Contains(got.Text, "banned") {
		t.Fatalf("notice message = %#v, want notice chat message", got)
	}
	if got := <-client.ConnectionStates(); got.Status != ConnectionConnected || !strings.Contains(got.Detail, "banned") {
		t.Fatalf("notice state = %#v, want connected notice detail", got)
	}

	transport.emit(twitch.Event{
		Kind: twitch.EventConnection,
		Connection: twitch.ConnectionEvent{
			Type:   twitch.ConnectionEventReconnect,
			At:     connectedAt.Add(2 * time.Second),
			Reason: ":tmi.twitch.tv RECONNECT",
		},
	})
	if got := <-client.ConnectionStates(); got.Status != ConnectionReconnecting || !strings.Contains(got.Detail, "RECONNECT") {
		t.Fatalf("reconnect state = %#v, want reconnect detail", got)
	}
}

func TestLiveChatClientAuthErrorsAreActionableAndRedacted(t *testing.T) {
	transport := newFakeTwitchTransport(2)
	client, err := NewLiveChatClient(context.Background(), transport, 2)
	if err != nil {
		t.Fatalf("NewLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	secretErr := errors.New("login authentication failed for oauth:secret-token")
	transport.emit(twitch.Event{
		Kind: twitch.EventConnection,
		Connection: twitch.ConnectionEvent{
			Type: twitch.ConnectionEventDisconnect,
			Err:  secretErr,
			At:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		},
		Err: secretErr,
	})

	got := <-client.ConnectionStates()
	if got.Status != ConnectionFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	for _, want := range []string{"OAuth token", "chat:read"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("detail %q missing %q", got.Detail, want)
		}
	}
	if strings.Contains(got.Detail, "oauth:secret-token") {
		t.Fatalf("detail leaked token: %q", got.Detail)
	}
}

func TestLiveChatClientTerminalFailureSurvivesEventStreamClose(t *testing.T) {
	transport := newFakeTwitchTransport(2)
	client, err := NewLiveChatClient(context.Background(), transport, 2)
	if err != nil {
		t.Fatalf("NewLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	transport.emit(twitch.Event{
		Kind: twitch.EventConnection,
		Connection: twitch.ConnectionEvent{
			Type: twitch.ConnectionEventDisconnect,
			Err:  errors.New("login authentication failed"),
			At:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		},
	})
	transport.Close()

	got := <-client.ConnectionStates()
	if got.Status != ConnectionFailed || !strings.Contains(got.Detail, "chat:read") {
		t.Fatalf("terminal state = %#v, want actionable failure", got)
	}
	next, ok := <-client.ConnectionStates()
	if ok {
		t.Fatalf("received extra state after terminal failure: %#v", next)
	}
}

func TestLiveChatClientRedactsOAuthPatternInGenericErrors(t *testing.T) {
	transport := newFakeTwitchTransport(2)
	client, err := NewLiveChatClient(context.Background(), transport, 2)
	if err != nil {
		t.Fatalf("NewLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	transport.emit(twitch.Event{
		Kind: twitch.EventError,
		Err:  errors.New("server rejected oauth:secret-token before welcome"),
	})

	got := <-client.ConnectionStates()
	if got.Status != ConnectionFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if strings.Contains(got.Detail, "oauth:secret-token") {
		t.Fatalf("detail leaked token: %q", got.Detail)
	}
	if !strings.Contains(got.Detail, "oauth:<redacted>") {
		t.Fatalf("detail = %q, want redacted token marker", got.Detail)
	}
}

func TestLiveChatClientSendRedactsTransportErrors(t *testing.T) {
	transport := newFakeTwitchTransport(2)
	transport.sendErr = errors.New("missing scope for oauth:secret-token")
	client, err := NewLiveChatClient(context.Background(), transport, 2)
	if err != nil {
		t.Fatalf("NewLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	_, err = client.Send(context.Background(), SendRequest{Channel: "example", Text: "hello"})
	if err == nil {
		t.Fatal("Send returned nil error, want transport error")
	}
	if strings.Contains(err.Error(), "oauth:secret-token") {
		t.Fatalf("Send error leaked token: %q", err.Error())
	}
	for _, want := range []string{"send failed", "chat:edit"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Send error = %q, want %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "chat:read") {
		t.Fatalf("Send error points at read scope instead of edit scope: %q", err.Error())
	}
}

func TestLiveShellConsumesClientMessagesAndConnectionStates(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("example", cfg, client, nil)

	state := ConnectionState{
		Status:  ConnectionReconnecting,
		Channel: "example",
		Detail:  "Twitch requested reconnect",
		At:      time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
	}
	updated, _ := model.Update(chatClientConnectionStateMsg{state: state, ok: true})
	model = updated.(mockShellModel)
	if !strings.Contains(model.View(), "reconnecting") || !strings.Contains(model.View(), "Twitch requested reconnect") {
		t.Fatalf("view missing reconnect state:\n%s", model.View())
	}

	msg := twitch.ChatMessage{
		ID:          "live-shell-1",
		Channel:     "example",
		Timestamp:   time.Date(2026, 7, 2, 12, 0, 1, 0, time.UTC),
		AuthorLogin: "viewer",
		DisplayName: "viewer",
		Text:        "live shell message",
		Type:        twitch.MessageTypeChat,
	}
	updated, cmd := model.Update(chatClientMessageMsg{message: msg, ok: true})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("client message did not schedule next client read")
	}
	if !strings.Contains(model.View(), "live shell message") {
		t.Fatalf("view missing live message:\n%s", model.View())
	}

	updated, _ = model.Update(tea.WindowSizeMsg{Width: 64, Height: 12})
	if got, want := lineCount(updated.View()), 12; got != want {
		t.Fatalf("live shell line count = %d, want %d:\n%s", got, want, updated.View())
	}
}

type fakeTwitchTransport struct {
	events chan twitch.Event

	mu        sync.Mutex
	connected bool
	closed    bool
	sendErr   error
	sends     []SendRequest
}

func newFakeTwitchTransport(buffer int) *fakeTwitchTransport {
	return &fakeTwitchTransport{events: make(chan twitch.Event, buffer)}
}

func (t *fakeTwitchTransport) Connect(context.Context) (<-chan twitch.Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = true
	return t.events, nil
}

func (t *fakeTwitchTransport) Send(_ context.Context, channel, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sends = append(t.sends, SendRequest{Channel: channel, Text: text})
	return t.sendErr
}

func (t *fakeTwitchTransport) Reply(context.Context, string, string, string) error {
	return nil
}

func (t *fakeTwitchTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.events)
	}
	return nil
}

func (t *fakeTwitchTransport) emit(event twitch.Event) {
	t.events <- event
}

func (t *fakeTwitchTransport) connectCalled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connected
}
