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

	userNoticeAt := connectedAt.Add(1500 * time.Millisecond)
	transport.emit(twitch.Event{
		Kind: twitch.EventUserNotice,
		UserNotice: twitch.UserNotice{
			ID:          "user-notice-1",
			Channel:     "example",
			RoomID:      "141981764",
			Timestamp:   userNoticeAt,
			AuthorLogin: "viewer",
			AuthorID:    "42",
			DisplayName: "Viewer",
			SystemText:  "Viewer subscribed.",
			Text:        "great stream",
		},
	})
	if got := <-client.Messages(); got.Type != twitch.MessageTypeNotice || got.ChannelID != "141981764" {
		t.Fatalf("user notice message = %#v, want room ID propagated", got)
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
	select {
	case next := <-client.ConnectionStates():
		t.Fatalf("received extra state after terminal failure: %#v", next)
	case <-time.After(20 * time.Millisecond):
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
	if !strings.Contains(got.Detail, "<redacted>") {
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

func TestRestartableLiveChatClientReconnectClosesOldTransportBeforeCreatingNext(t *testing.T) {
	factory := &fakeRestartTransportFactory{}
	first := factory.queueTransport(newFakeTwitchTransport(4))
	second := factory.queueTransport(newFakeTwitchTransport(4))

	client, err := NewRestartableLiveChatClient(context.Background(), factory.newTransport, 8)
	if err != nil {
		t.Fatalf("NewRestartableLiveChatClient returned error: %v", err)
	}
	defer client.Close()

	if got := <-client.ConnectionStates(); got.Status != ConnectionConnecting {
		t.Fatalf("initial state = %#v, want connecting", got)
	}
	if !first.connectCalled() {
		t.Fatal("first transport was not connected")
	}

	if err := client.Reconnect(context.Background()); err != nil {
		t.Fatalf("Reconnect returned error: %v", err)
	}
	if !first.closedCalled() {
		t.Fatal("first transport was not closed before reconnect completed")
	}
	if factory.createdBeforePreviousClose() {
		t.Fatal("factory created a replacement transport before the previous transport was closed")
	}
	if !second.connectCalled() {
		t.Fatal("second transport was not connected")
	}

	if got := <-client.ConnectionStates(); got.Status != ConnectionReconnecting || !strings.Contains(got.Detail, "manual reconnect") {
		t.Fatalf("reconnect state = %#v, want manual reconnect feedback", got)
	}

	second.emit(twitch.Event{
		Kind: twitch.EventConnection,
		Connection: twitch.ConnectionEvent{
			Type: twitch.ConnectionEventConnect,
			At:   time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC),
		},
	})
	if got := <-client.ConnectionStates(); got.Status != ConnectionConnected {
		t.Fatalf("post-reconnect state = %#v, want connected from replacement transport", got)
	}
}

func TestRestartableLiveChatClientReconnectFailureReportsRetryAndAllowsNextAttempt(t *testing.T) {
	factory := &fakeRestartTransportFactory{}
	first := factory.queueTransport(newFakeTwitchTransport(4))
	factory.queueError(errors.New("dial failed access_token=secret-token&client_secret=super-secret"))
	second := factory.queueTransport(newFakeTwitchTransport(4))

	client, err := NewRestartableLiveChatClient(context.Background(), factory.newTransport, 8)
	if err != nil {
		t.Fatalf("NewRestartableLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	err = client.Reconnect(context.Background())
	if err == nil {
		t.Fatal("Reconnect returned nil error, want factory failure")
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("Reconnect error leaked token: %q", err.Error())
	}
	if !first.closedCalled() {
		t.Fatal("first transport was not closed before reconnect failure")
	}
	if got := <-client.ConnectionStates(); got.Status != ConnectionReconnecting {
		t.Fatalf("first reconnect state = %#v, want reconnecting", got)
	}
	got := <-client.ConnectionStates()
	if got.Status != ConnectionFailed || !strings.Contains(got.Detail, "retry with ctrl+r") {
		t.Fatalf("failure state = %#v, want retry guidance", got)
	}
	if strings.Contains(got.Detail, "secret-token") || strings.Contains(got.Detail, "super-secret") {
		t.Fatalf("failure state leaked token: %q", got.Detail)
	}
	_, err = client.Send(context.Background(), SendRequest{Channel: "example", Text: "hello"})
	if !errors.Is(err, ErrLiveChatDisconnected) {
		t.Fatalf("Send error after failed reconnect = %v, want %v", err, ErrLiveChatDisconnected)
	}

	if err := client.Reconnect(context.Background()); err != nil {
		t.Fatalf("second Reconnect returned error: %v", err)
	}
	if !second.connectCalled() {
		t.Fatal("second transport was not connected after retry")
	}
}

func TestRestartableLiveChatClientReconnectHonorsCanceledContext(t *testing.T) {
	factory := &fakeRestartTransportFactory{}
	first := factory.queueTransport(newFakeTwitchTransport(4))

	client, err := NewRestartableLiveChatClient(context.Background(), factory.newTransport, 8)
	if err != nil {
		t.Fatalf("NewRestartableLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = client.Reconnect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconnect error = %v, want %v", err, context.Canceled)
	}
	if first.closedCalled() {
		t.Fatal("canceled reconnect closed the active transport")
	}
	if got, want := factory.calls(), 1; got != want {
		t.Fatalf("factory calls = %d, want %d", got, want)
	}
}

func TestRestartableLiveChatClientReconnectCancellationClosesUnconnectedReplacement(t *testing.T) {
	first := newFakeTwitchTransport(4)
	second := newFakeTwitchTransport(4)
	var (
		mu     sync.Mutex
		calls  int
		cancel context.CancelFunc
	)
	factory := func(context.Context) (twitch.ChatClient, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return first, nil
		}
		cancel()
		return second, nil
	}

	client, err := NewRestartableLiveChatClient(context.Background(), factory, 8)
	if err != nil {
		t.Fatalf("NewRestartableLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	ctx, cancelFunc := context.WithCancel(context.Background())
	cancel = cancelFunc
	err = client.Reconnect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconnect error = %v, want %v", err, context.Canceled)
	}
	if !first.closedCalled() {
		t.Fatal("first transport was not closed before canceled replacement")
	}
	if !second.closedCalled() {
		t.Fatal("replacement transport was not closed after cancellation")
	}
	if second.connectCalled() {
		t.Fatal("replacement transport connected after reconnect context was canceled")
	}
}

func TestRestartableLiveChatClientReconnectCancellationClosesBlockingConnect(t *testing.T) {
	first := newFakeTwitchTransport(4)
	second := newBlockingConnectTransport()
	var (
		mu    sync.Mutex
		calls int
	)
	factory := func(context.Context) (twitch.ChatClient, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}

	client, err := NewRestartableLiveChatClient(context.Background(), factory, 8)
	if err != nil {
		t.Fatalf("NewRestartableLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Reconnect(ctx)
	}()
	<-second.connectEntered
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Reconnect error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("Reconnect did not return after context cancellation")
	}
	if !first.closedCalled() {
		t.Fatal("first transport was not closed before blocking replacement")
	}
	if !second.closedCalled() {
		t.Fatal("blocking replacement transport was not closed after cancellation")
	}
}

func TestRestartableLiveChatClientRejectsRepeatedReconnectWhileInProgress(t *testing.T) {
	factory := &fakeRestartTransportFactory{}
	factory.queueTransport(newFakeTwitchTransport(4))
	second := factory.queueTransport(newFakeTwitchTransport(4))
	block := make(chan struct{})
	entered := make(chan struct{})
	factory.blockCall(2, entered, block)

	client, err := NewRestartableLiveChatClient(context.Background(), factory.newTransport, 8)
	if err != nil {
		t.Fatalf("NewRestartableLiveChatClient returned error: %v", err)
	}
	defer client.Close()
	<-client.ConnectionStates()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Reconnect(context.Background())
	}()
	<-entered

	if err := client.Reconnect(context.Background()); !errors.Is(err, ErrReconnectInProgress) {
		t.Fatalf("second Reconnect error = %v, want %v", err, ErrReconnectInProgress)
	}
	close(block)
	if err := <-errCh; err != nil {
		t.Fatalf("first Reconnect returned error: %v", err)
	}
	if !second.connectCalled() {
		t.Fatal("blocked reconnect did not connect replacement transport after release")
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

func TestLiveShellRoutesInactiveMessagesWithoutStealingFocusOrScroll(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("alpha", cfg, client, nil)
	alpha := model.channels.ensure("alpha")
	beta := model.channels.ensure("beta")
	alpha.messages = numberedMockMessages("alpha", 40)
	alpha.scrollOffset = 3
	model.focus = mockFocusComposer
	alpha.composerText = "active draft"

	updated, cmd := model.Update(chatClientMessageMsg{
		message: mockIncomingMessage("#beta", "beta-live-1", "beta stays inactive"),
		ok:      true,
	})
	model = updated.(mockShellModel)
	if cmd == nil {
		t.Fatal("client message did not schedule next client read")
	}
	if got, want := model.activeChannelName(), "alpha"; got != want {
		t.Fatalf("active channel = %q, want %q", got, want)
	}
	if got, want := model.focus, mockFocusComposer; got != want {
		t.Fatalf("focus = %v, want composer", got)
	}
	if got, want := alpha.scrollOffset, 3; got != want {
		t.Fatalf("alpha scrollOffset = %d, want %d", got, want)
	}
	if got, want := alpha.composerText, "active draft"; got != want {
		t.Fatalf("alpha composerText = %q, want %q", got, want)
	}
	if got, want := beta.unread, 1; got != want {
		t.Fatalf("beta unread = %d, want %d", got, want)
	}
	if got, want := len(beta.messages), 1; got != want {
		t.Fatalf("beta messages = %d, want %d", got, want)
	}
	if got, want := beta.messages[0].Channel, "beta"; got != want {
		t.Fatalf("beta message channel = %q, want normalized %q", got, want)
	}
	view := model.View()
	if !strings.Contains(view, "unread=1") {
		t.Fatalf("active view missing unread indicator:\n%s", view)
	}
	if strings.Contains(view, "beta stays inactive") {
		t.Fatalf("inactive beta message stole active view:\n%s", view)
	}
}

func TestLiveShellAppliesGlobalConnectionEventsToConfiguredChannels(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultChannels = []string{"alpha", "beta"}
	client := NewFakeChatClient(1)
	model := newLiveShellModelWithClock("alpha", cfg, client, nil)

	updated, _ := model.Update(chatClientConnectionStateMsg{
		state: ConnectionState{
			Status: ConnectionConnected,
			Detail: "joined Twitch IRC",
			At:     time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		},
		ok: true,
	})
	model = updated.(mockShellModel)

	for _, channel := range []string{"alpha", "beta"} {
		state := model.channels.states[channelKey(channel)]
		if state == nil {
			t.Fatalf("missing channel state %q", channel)
		}
		if state.status.Status != ConnectionConnected || state.status.Channel != channel {
			t.Fatalf("%s status = %#v, want connected channel-scoped copy", channel, state.status)
		}
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

func (t *fakeTwitchTransport) closedCalled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
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

type blockingConnectTransport struct {
	connectEntered chan struct{}
	closedCh       chan struct{}
	closeOnce      sync.Once

	mu     sync.Mutex
	closed bool
}

func newBlockingConnectTransport() *blockingConnectTransport {
	return &blockingConnectTransport{
		connectEntered: make(chan struct{}),
		closedCh:       make(chan struct{}),
	}
}

func (t *blockingConnectTransport) Connect(ctx context.Context) (<-chan twitch.Event, error) {
	close(t.connectEntered)
	select {
	case <-t.closedCh:
		return nil, ErrLiveChatClientClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *blockingConnectTransport) Send(context.Context, string, string) error {
	return nil
}

func (t *blockingConnectTransport) Reply(context.Context, string, string, string) error {
	return nil
}

func (t *blockingConnectTransport) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		close(t.closedCh)
	})
	return nil
}

func (t *blockingConnectTransport) closedCalled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

type fakeRestartTransportFactory struct {
	mu                          sync.Mutex
	results                     []fakeFactoryResult
	created                     []*fakeTwitchTransport
	callsValue                  int
	createdBeforePreviousClosed bool
	blockedCalls                map[int]fakeFactoryBlock
}

type fakeFactoryResult struct {
	transport *fakeTwitchTransport
	err       error
}

type fakeFactoryBlock struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (f *fakeRestartTransportFactory) queueTransport(transport *fakeTwitchTransport) *fakeTwitchTransport {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, fakeFactoryResult{transport: transport})
	return transport
}

func (f *fakeRestartTransportFactory) queueError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, fakeFactoryResult{err: err})
}

func (f *fakeRestartTransportFactory) blockCall(call int, entered chan<- struct{}, release <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blockedCalls == nil {
		f.blockedCalls = make(map[int]fakeFactoryBlock)
	}
	f.blockedCalls[call] = fakeFactoryBlock{entered: entered, release: release}
}

func (f *fakeRestartTransportFactory) newTransport(ctx context.Context) (twitch.ChatClient, error) {
	f.mu.Lock()
	f.callsValue++
	call := f.callsValue
	block := f.blockedCalls[call]
	f.mu.Unlock()

	if block.entered != nil {
		close(block.entered)
	}
	if block.release != nil {
		select {
		case <-block.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) > 0 {
		previous := f.created[len(f.created)-1]
		if previous != nil && !previous.closedCalled() {
			f.createdBeforePreviousClosed = true
		}
	}
	if len(f.results) == 0 {
		return nil, errors.New("missing queued transport")
	}
	result := f.results[0]
	f.results = f.results[1:]
	if result.err != nil {
		return nil, result.err
	}
	transport := result.transport
	if transport == nil {
		return nil, errors.New("missing queued transport")
	}
	f.created = append(f.created, transport)
	return transport, nil
}

func (f *fakeRestartTransportFactory) createdBeforePreviousClose() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createdBeforePreviousClosed
}

func (f *fakeRestartTransportFactory) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callsValue
}
