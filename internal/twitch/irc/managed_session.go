package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	gempir "github.com/gempir/go-twitch-irc/v4"
	"github.com/worxbend/twi/internal/sendquota"
)

// managedSession uses the maintained upstream parser while giving twi sole
// ownership of socket lifetime. A failed session never opens a hidden replacement.
type managedSession struct {
	Capabilities              []string
	username, token           string
	writeMu                   sync.Mutex
	mu                        sync.Mutex
	channels                  []string
	registered, started       bool
	conn                      net.Conn
	cancel                    context.CancelFunc
	terminal                  error
	closed                    bool
	outgoing                  chan string
	controls                  chan string
	joins                     chan string
	dial                      func(context.Context) (net.Conn, error)
	reserve                   func(context.Context, string, int) error
	pingInterval, readTimeout time.Duration
	onConnect                 func()
	onPrivateMessage          func(gempir.PrivateMessage)
	onNoticeMessage           func(gempir.NoticeMessage)
	onUserNoticeMessage       func(gempir.UserNoticeMessage)
	onRoomStateMessage        func(gempir.RoomStateMessage)
	onClearChatMessage        func(gempir.ClearChatMessage)
	onClearMessage            func(gempir.ClearMessage)
	onUserStateMessage        func(gempir.UserStateMessage)
	onUserJoinMessage         func(gempir.UserJoinMessage)
	onUserPartMessage         func(gempir.UserPartMessage)
	onReconnectMessage        func(gempir.ReconnectMessage)
	onUnsetMessage            func(gempir.RawMessage)
}

var _ chatSession = (*managedSession)(nil)

func newManagedSession(username, token string, channels []string) *managedSession {
	session := &managedSession{username: username, token: token, channels: normalizeChannels(channels), outgoing: make(chan string, 128), controls: make(chan string, 16), joins: make(chan string, 100), Capabilities: []string{gempir.TagsCapability, gempir.CommandsCapability, gempir.MembershipCapability}, pingInterval: 30 * time.Second, readTimeout: 90 * time.Second}
	session.dial = func(ctx context.Context) (net.Conn, error) {
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 15 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12}}
		return dialer.DialContext(ctx, "tcp", "irc.chat.twitch.tv:6697")
	}
	session.reserve = func(ctx context.Context, kind string, count int) error {
		return sendquota.AcquireConnection(ctx, strings.ToLower(username), kind, count)
	}
	return session
}
func (s *managedSession) Connect() error { return s.ConnectContext(context.Background()) }
func (s *managedSession) ConnectContext(parent context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("IRC session Connect called more than once")
	}
	if s.closed {
		s.mu.Unlock()
		return gempir.ErrClientDisconnected
	}
	s.started = true
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.mu.Unlock()
	defer cancel()
	if err := s.reserve(ctx, "auth", 1); err != nil {
		return err
	}
	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return gempir.ErrClientDisconnected
	}
	s.conn = conn
	s.mu.Unlock()
	defer conn.Close()
	// Cancellation closes the socket, including a blocked read or write.
	stopped := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopped:
		}
	}()
	defer func() { close(stopped); <-watchDone }()
	for _, line := range []string{"CAP REQ :" + strings.Join(s.Capabilities, " "), "PASS " + s.token, "NICK " + s.username} {
		if err = s.write(conn, line); err != nil {
			return s.result(ctx, err)
		}
	}
	workers := sync.WaitGroup{}
	workers.Add(3)
	go func() { defer workers.Done(); s.writer(ctx, conn) }()
	go func() { defer workers.Done(); s.joiner(ctx) }()
	go func() { defer workers.Done(); s.pinger(ctx) }()
	defer func() { cancel(); _ = conn.Close(); workers.Wait() }()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for {
		if err = conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
			return s.result(ctx, err)
		}
		if !scanner.Scan() {
			err = scanner.Err()
			if err == nil {
				err = io.EOF
			}
			return s.result(ctx, err)
		}
		parsed := gempir.ParseMessage(scanner.Text())
		switch message := parsed.(type) {
		case *gempir.PingMessage:
			s.enqueue(ctx, s.controls, "PONG :"+message.Message)
		case *gempir.PongMessage:
		case *gempir.RawMessage:
			if message.RawType == "001" {
				s.mu.Lock()
				first := !s.registered
				s.registered = true
				channels := append([]string(nil), s.channels...)
				s.mu.Unlock()
				if first {
					if s.onConnect != nil {
						s.onConnect()
					}
					for _, channel := range channels {
						s.enqueue(ctx, s.joins, channel)
					}
				}
			} else if s.onUnsetMessage != nil {
				s.onUnsetMessage(*message)
			}
		case *gempir.NoticeMessage:
			if s.onNoticeMessage != nil {
				s.onNoticeMessage(*message)
			}
			if NormalizeNoticeMessage(*message).Notice.AuthFailed {
				return gempir.ErrLoginAuthenticationFailed
			}
		case *gempir.ReconnectMessage:
			if s.onReconnectMessage != nil {
				s.onReconnectMessage(*message)
			}
			return errors.New("twitch requested IRC reconnect")
		case *gempir.PrivateMessage:
			if s.onPrivateMessage != nil {
				s.onPrivateMessage(*message)
			}
		case *gempir.UserNoticeMessage:
			if s.onUserNoticeMessage != nil {
				s.onUserNoticeMessage(*message)
			}
		case *gempir.RoomStateMessage:
			if s.onRoomStateMessage != nil {
				s.onRoomStateMessage(*message)
			}
		case *gempir.ClearChatMessage:
			if s.onClearChatMessage != nil {
				s.onClearChatMessage(*message)
			}
		case *gempir.ClearMessage:
			if s.onClearMessage != nil {
				s.onClearMessage(*message)
			}
		case *gempir.UserStateMessage:
			if s.onUserStateMessage != nil {
				s.onUserStateMessage(*message)
			}
		case *gempir.UserJoinMessage:
			if s.onUserJoinMessage != nil {
				s.onUserJoinMessage(*message)
			}
		case *gempir.UserPartMessage:
			if s.onUserPartMessage != nil {
				s.onUserPartMessage(*message)
			}
		}
	}
}
func (s *managedSession) result(ctx context.Context, fallback error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return s.terminal
	}
	if s.closed {
		return gempir.ErrClientDisconnected
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fallback
}
func (s *managedSession) fail(err error) {
	s.mu.Lock()
	if s.terminal == nil {
		s.terminal = err
	}
	cancel := s.cancel
	conn := s.conn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
}
func (s *managedSession) Disconnect() error {
	s.mu.Lock()
	s.closed = true
	cancel := s.cancel
	conn := s.conn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}
func (s *managedSession) write(conn net.Conn, line string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writeUnlocked(conn, line)
}
func (s *managedSession) writeUnlocked(conn net.Conn, line string) error {
	if err := conn.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	payload := line + "\r\n"
	n, err := io.WriteString(conn, payload)
	if err == nil && n != len(payload) {
		err = io.ErrShortWrite
	}
	return err
}
func (s *managedSession) enqueue(ctx context.Context, queue chan string, line string) {
	select {
	case queue <- line:
	case <-ctx.Done():
	default:
		s.fail(errors.New("IRC outbound queue full; session closed"))
	}
}
func (s *managedSession) writer(ctx context.Context, conn net.Conn) {
	for {
		var line string
		// Keepalives precede buffered chat and channel commands.
		select {
		case <-ctx.Done():
			return
		case line = <-s.controls:
		default:
		}
		if line == "" {
			select {
			case <-ctx.Done():
				return
			case line = <-s.controls:
			case line = <-s.outgoing:
			}
		}
		if err := s.write(conn, line); err != nil {
			s.fail(err)
			return
		}
	}
}
func (s *managedSession) joiner(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case channel := <-s.joins:
			if err := s.reserve(ctx, "joins", 1); err != nil {
				if ctx.Err() == nil {
					s.fail(err)
				}
				return
			}
			s.writeMu.Lock()
			s.mu.Lock()
			wanted := containsChannel(s.channels, channel)
			conn := s.conn
			s.mu.Unlock()
			var err error
			if wanted && conn != nil {
				err = s.writeUnlocked(conn, "JOIN #"+channel)
			}
			s.writeMu.Unlock()
			if err != nil {
				s.fail(err)
				return
			}
		}
	}
}
func (s *managedSession) pinger(ctx context.Context) {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.enqueue(ctx, s.controls, "PING :twi")
		}
	}
}
func (s *managedSession) Join(channels ...string) {
	s.mu.Lock()
	var added []string
	for _, channel := range normalizeChannels(channels) {
		if !containsChannel(s.channels, channel) {
			if len(s.channels) >= 100 {
				s.mu.Unlock()
				s.fail(errors.New("IRC channel limit exceeded"))
				return
			}
			s.channels = append(s.channels, channel)
			added = append(added, channel)
		}
	}
	active := s.registered
	ctx := context.Background()
	s.mu.Unlock()
	if active {
		for _, channel := range added {
			s.enqueue(ctx, s.joins, channel)
		}
	}
}
func (s *managedSession) Depart(channel string) {
	channel = normalizeChannel(channel)
	s.mu.Lock()
	remaining := s.channels[:0]
	for _, current := range s.channels {
		if current != channel {
			remaining = append(remaining, current)
		}
	}
	s.channels = remaining
	active := s.registered
	s.mu.Unlock()
	if active {
		s.enqueue(context.Background(), s.outgoing, "PART #"+channel)
	}
}
func (s *managedSession) Say(channel, text string) {
	s.enqueue(context.Background(), s.outgoing, "PRIVMSG #"+channel+" :"+truncateChatMessage(text))
}
func (s *managedSession) Reply(channel, parent, text string) {
	if parent == "" || strings.ContainsAny(parent, " ;\\\t\r\n") {
		s.fail(errors.New("invalid IRC reply message identity"))
		return
	}
	for _, r := range parent {
		if r < 0x20 || r == 0x7f {
			s.fail(errors.New("invalid IRC reply message identity"))
			return
		}
	}
	s.enqueue(context.Background(), s.outgoing, "@reply-parent-msg-id="+parent+" PRIVMSG #"+channel+" :"+truncateChatMessage(text))
}

func (s *managedSession) OnConnect(callback func()) { s.onConnect = callback }

func (s *managedSession) OnPrivateMessage(callback func(gempir.PrivateMessage)) {
	s.onPrivateMessage = callback
}

func (s *managedSession) OnNoticeMessage(callback func(gempir.NoticeMessage)) {
	s.onNoticeMessage = callback
}

func (s *managedSession) OnUserNoticeMessage(callback func(gempir.UserNoticeMessage)) {
	s.onUserNoticeMessage = callback
}

func (s *managedSession) OnRoomStateMessage(callback func(gempir.RoomStateMessage)) {
	s.onRoomStateMessage = callback
}

func (s *managedSession) OnClearChatMessage(callback func(gempir.ClearChatMessage)) {
	s.onClearChatMessage = callback
}

func (s *managedSession) OnClearMessage(callback func(gempir.ClearMessage)) {
	s.onClearMessage = callback
}

func (s *managedSession) OnUserStateMessage(callback func(gempir.UserStateMessage)) {
	s.onUserStateMessage = callback
}

func (s *managedSession) OnUserJoinMessage(callback func(gempir.UserJoinMessage)) {
	s.onUserJoinMessage = callback
}

func (s *managedSession) OnUserPartMessage(callback func(gempir.UserPartMessage)) {
	s.onUserPartMessage = callback
}

func (s *managedSession) OnReconnectMessage(callback func(gempir.ReconnectMessage)) {
	s.onReconnectMessage = callback
}

func (s *managedSession) OnUnsetMessage(callback func(gempir.RawMessage)) {
	s.onUnsetMessage = callback
}
