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

func TestLiveChatClientSendReplyUsesTransportReply(t *testing.T) {
	transport := newFakeTwitchTransport(2)
	client, err := NewLiveChatClient(context.Background(), transport, 2)
	if err != nil {
		t.Fatalf("NewLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	_, err = client.Send(context.Background(), SendRequest{
		Channel:          "example",
		Text:             "thanks",
		ReplyToMessageID: "parent-1",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	replies := transport.repliesSent()
	if len(replies) != 1 {
		t.Fatalf("replies length = %d, want 1", len(replies))
	}
	if replies[0] != (SendRequest{Channel: "example", Text: "thanks", ReplyToMessageID: "parent-1"}) {
		t.Fatalf("reply request = %#v, want transport reply with parent ID", replies[0])
	}
	if got := transport.sendsSent(); len(got) != 0 {
		t.Fatalf("normal sends length = %d, want 0 for reply path", len(got))
	}
}

func TestLiveChatClientSendActionUsesIRCActionText(t *testing.T) {
	transport := newFakeTwitchTransport(2)
	client, err := NewLiveChatClient(context.Background(), transport, 2)
	if err != nil {
		t.Fatalf("NewLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	_, err = client.Send(context.Background(), SendRequest{
		Channel: "example",
		Text:    "waves at chat",
		Action:  true,
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	sends := transport.sendsSent()
	if len(sends) != 1 {
		t.Fatalf("sends length = %d, want 1", len(sends))
	}
	if got, want := sends[0].Text, "\x01ACTION waves at chat\x01"; got != want {
		t.Fatalf("action wire text = %q, want %q", got, want)
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

func TestLiveShellKeepsPerChannelHistoryStatusUnreadAndSwitching(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("alpha", cfg, client, nil)

	alphaState := ConnectionState{
		Status:  ConnectionConnected,
		Channel: "alpha",
		Detail:  "alpha connected",
		At:      time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
	}
	betaState := ConnectionState{
		Status:  ConnectionReconnecting,
		Channel: "beta",
		Detail:  "beta reconnecting",
		At:      time.Date(2026, 7, 2, 12, 0, 1, 0, time.UTC),
	}
	for _, state := range []ConnectionState{alphaState, betaState} {
		updated, _ := model.Update(chatClientConnectionStateMsg{state: state, ok: true})
		model = updated.(mockShellModel)
	}

	alphaMessage := mockIncomingMessage("alpha", "alpha-1", "alpha active history")
	betaMessage := mockIncomingMessage("beta", "beta-1", "beta inactive history")
	for _, msg := range []twitch.ChatMessage{alphaMessage, betaMessage} {
		updated, _ := model.Update(chatClientMessageMsg{message: msg, ok: true})
		model = updated.(mockShellModel)
	}

	alpha := model.channels.states[channelKey("alpha")]
	beta := model.channels.states[channelKey("beta")]
	if alpha == nil || beta == nil {
		t.Fatalf("channel states = %#v, want alpha and beta", model.channels.channelNames())
	}
	if got, want := len(alpha.messages), 1; got != want {
		t.Fatalf("alpha history length = %d, want %d: %#v", got, want, alpha.messages)
	}
	if got, want := len(beta.messages), 1; got != want {
		t.Fatalf("beta history length = %d, want %d: %#v", got, want, beta.messages)
	}
	if !messagesContainText(alpha.messages, "alpha active history") || messagesContainText(alpha.messages, "beta inactive history") {
		t.Fatalf("alpha history not isolated: %#v", alpha.messages)
	}
	if !messagesContainText(beta.messages, "beta inactive history") || messagesContainText(beta.messages, "alpha active history") {
		t.Fatalf("beta history not isolated: %#v", beta.messages)
	}
	if got, want := beta.unread, 1; got != want {
		t.Fatalf("beta unread = %d, want %d", got, want)
	}
	view := model.View()
	for _, want := range []string{"#alpha connected", "alpha connected", "unread=1", "alpha active history"} {
		if !strings.Contains(view, want) {
			t.Fatalf("active alpha view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "beta inactive history") {
		t.Fatalf("inactive beta history leaked into alpha view:\n%s", view)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(mockShellModel)
	if cmd != nil {
		t.Fatalf("channel switch returned command %#v, want nil without visible assets", cmd)
	}
	if got, want := model.activeChannelName(), "beta"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
	if got := beta.unread; got != 0 {
		t.Fatalf("beta unread after activation = %d, want 0", got)
	}
	view = model.View()
	for _, want := range []string{"#beta reconnecting", "beta reconnecting", "beta inactive history"} {
		if !strings.Contains(view, want) {
			t.Fatalf("active beta view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "alpha active history") || strings.Contains(view, "unread=1") {
		t.Fatalf("alpha history or stale unread leaked into beta view:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(mockShellModel)
	if got, want := model.activeChannelName(), "alpha"; got != want {
		t.Fatalf("active channel after wrap = %q, want %q", got, want)
	}
	if view := model.View(); !strings.Contains(view, "alpha active history") || strings.Contains(view, "beta inactive history") {
		t.Fatalf("wrapped alpha view has wrong history:\n%s", view)
	}
}

type fakeTwitchTransport struct {
	events chan twitch.Event

	mu        sync.Mutex
	connected bool
	closed    bool
	sendErr   error
	sends     []SendRequest
	replies   []SendRequest
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

func (t *fakeTwitchTransport) Reply(_ context.Context, channel, parentMessageID, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.replies = append(t.replies, SendRequest{Channel: channel, Text: text, ReplyToMessageID: parentMessageID})
	return t.sendErr
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

func (t *fakeTwitchTransport) sendsSent() []SendRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SendRequest, len(t.sends))
	copy(out, t.sends)
	return out
}

func (t *fakeTwitchTransport) repliesSent() []SendRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SendRequest, len(t.replies))
	copy(out, t.replies)
	return out
}
