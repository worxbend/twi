package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
)

// Producer writes PRIVMSG synchronously. A completed TCP/TLS write is unconfirmed,
// not a Twitch acceptance receipt. It never uses the UI adapter's enqueue queue.
type Producer struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	config  ProducerConfig
	session *producerSession
}
type ProducerConfig struct {
	Login       string
	Token       func() string
	Dial        func(context.Context) (net.Conn, error)
	ReserveJoin func(context.Context, string) error
	ReserveAuth func(context.Context, string) error
	Lease       func(context.Context, string) (*sendquota.ChannelLease, error)
}
type producerSession struct {
	conn      net.Conn
	token     string
	done      chan struct{}
	welcome   chan error
	roomsMu   sync.Mutex
	rooms     map[string]chan struct{}
	leases    map[string]*sendquota.ChannelLease
	closeOnce sync.Once
}

func NewProducer(ctx context.Context, config ProducerConfig) (*Producer, error) {
	channel, err := twitch.ParseChannel(config.Login)
	if err != nil || config.Token == nil {
		return nil, errors.New("IRC producer requires validated login and token source")
	}
	config.Login = channel.String()
	if config.Dial == nil {
		config.Dial = func(ctx context.Context) (net.Conn, error) {
			dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 15 * time.Second}, Config: &tls.Config{ServerName: "irc.chat.twitch.tv", MinVersion: tls.VersionTLS12}}
			return dialer.DialContext(ctx, "tcp", "irc.chat.twitch.tv:6697")
		}
	}
	if config.ReserveAuth == nil {
		config.ReserveAuth = func(ctx context.Context, login string) error {
			return sendquota.AcquireConnection(ctx, login, "auth", 1)
		}
	}
	if config.ReserveJoin == nil {
		config.ReserveJoin = func(ctx context.Context, login string) error {
			return sendquota.AcquireConnection(ctx, login, "joins", 1)
		}
	}
	if config.Lease == nil {
		config.Lease = func(ctx context.Context, channel string) (*sendquota.ChannelLease, error) {
			return sendquota.ReserveChannels(ctx, config.Login, []string{channel})
		}
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &Producer{ctx: lifetime, cancel: cancel, config: config}, nil
}
func (p *Producer) Close() error {
	p.cancel()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeSession(p.session)
	p.session = nil
	return nil
}
func (p *Producer) closeSession(s *producerSession) {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		_ = s.conn.Close()
		s.roomsMu.Lock()
		for _, lease := range s.leases {
			if lease != nil {
				_ = lease.Close()
			}
		}
		s.roomsMu.Unlock()
	})
}
func (p *Producer) Send(ctx context.Context, message twitch.OutboundMessage) error {
	if err := validateProducerWire(message); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	channel, err := twitch.ParseChannel(message.Channel)
	if err != nil {
		return err
	}
	if p.session != nil {
		select {
		case <-p.session.done:
			p.closeSession(p.session)
			p.session = nil
		default:
		}
	}
	token := twitch.NormalizeIRCOAuthToken(p.config.Token())
	if token == "" {
		return twitch.ErrAuthFailed
	}
	if p.session == nil || p.session.token != token {
		p.closeSession(p.session)
		p.session = nil
		if err := p.connect(ctx, token); err != nil {
			return err
		}
	}
	if strings.ContainsAny(message.ReplyToMessageID, " ;\\\t\r\n") {
		return errors.New("invalid IRC reply identifier")
	}
	session := p.session
	select {
	case <-session.done:
		return errors.New("IRC producer connection ended before write")
	default:
	}
	session.roomsMu.Lock()
	ready, exists := session.rooms[channel.String()]
	session.roomsMu.Unlock()
	if !exists {
		session.roomsMu.Lock()
		count := len(session.rooms)
		session.roomsMu.Unlock()
		if count >= 100 {
			return errors.New("IRC producer channel selection exceeds 100")
		}
		lease, err := p.config.Lease(ctx, channel.String())
		if err != nil {
			return err
		}
		if err := p.config.ReserveJoin(ctx, p.config.Login); err != nil {
			if lease != nil {
				_ = lease.Close()
			}
			return err
		}
		ready = make(chan struct{})
		session.roomsMu.Lock()
		session.rooms[channel.String()] = ready
		session.leases[channel.String()] = lease
		session.roomsMu.Unlock()
		if err := p.write(ctx, session, "JOIN #"+channel.String()); err != nil {
			return err
		}
	}
	select {
	case <-ready:
	case <-session.done:
		return errors.New("IRC channel readiness failed")
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
	text := message.Text
	if message.Action {
		text = actionWireTextPreservingSpaces(text)
	}
	command := "PRIVMSG #" + channel.String() + " :" + text
	if message.ReplyToMessageID != "" {
		command = "@reply-parent-msg-id=" + message.ReplyToMessageID + " " + command
	}
	err = p.write(ctx, session, command)
	if err != nil {
		p.closeSession(session)
		p.session = nil
	}
	return err
}
func actionWireTextPreservingSpaces(text string) string { return "\x01ACTION " + text + "\x01" }
func (p *Producer) write(ctx context.Context, s *producerSession, line string) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	_ = s.conn.SetWriteDeadline(deadline)
	finished := make(chan struct{})
	stopped := context.AfterFunc(ctx, func() { _ = s.conn.SetWriteDeadline(time.Now()); close(finished) })
	defer func() {
		if !stopped() {
			<-finished
		}
	}()
	data := []byte(line + "\r\n")
	written, err := s.conn.Write(data)
	if err != nil || written != len(data) {
		return errors.New("IRC write outcome unknown")
	}
	return nil
}
func (p *Producer) connect(ctx context.Context, token string) error {
	if err := p.config.ReserveAuth(ctx, p.config.Login); err != nil {
		return err
	}
	conn, err := p.config.Dial(ctx)
	if err != nil {
		return errors.New("IRC producer connection failed")
	}
	session := &producerSession{conn: conn, token: token, done: make(chan struct{}), welcome: make(chan error, 1), rooms: map[string]chan struct{}{}, leases: map[string]*sendquota.ChannelLease{}}
	stop := context.AfterFunc(p.ctx, func() { p.closeSession(session) })
	go func() {
		defer stop()
		defer close(session.done)
		defer p.closeSession(session)
		reader := bufio.NewScanner(conn)
		reader.Buffer(make([]byte, 4096), 64*1024)
		for reader.Scan() {
			line := reader.Text()
			command, params := producerCommand(line)
			if command == "PING" {
				bounded, cancel := context.WithTimeout(p.ctx, 5*time.Second)
				_ = p.write(bounded, session, "PONG "+strings.Join(params, " "))
				cancel()
				continue
			}
			if command == "001" {
				select {
				case session.welcome <- nil:
				default:
				}
			}
			if command == "NOTICE" && (strings.Contains(strings.ToLower(line), "authentication failed") || strings.Contains(strings.ToLower(line), "improperly formatted auth")) {
				select {
				case session.welcome <- twitch.ErrAuthFailed:
				default:
				}
				return
			}
			if command == "ROOMSTATE" && len(params) > 0 {
				room := twitch.ChannelKey(params[0])
				session.roomsMu.Lock()
				if ready := session.rooms[room]; ready != nil {
					select {
					case <-ready:
					default:
						close(ready)
					}
				}
				session.roomsMu.Unlock()
			}
		}
	}()
	for _, line := range []string{"CAP REQ :twitch.tv/tags twitch.tv/commands", "PASS " + token, "NICK " + p.config.Login} {
		if err := p.write(ctx, session, line); err != nil {
			p.closeSession(session)
			return err
		}
	}
	select {
	case err := <-session.welcome:
		if err != nil {
			p.closeSession(session)
			return err
		}
	case <-session.done:
		return fmt.Errorf("IRC authentication did not complete: %w", twitch.ErrAuthFailed)
	case <-ctx.Done():
		p.closeSession(session)
		return ctx.Err()
	case <-p.ctx.Done():
		p.closeSession(session)
		return p.ctx.Err()
	}
	p.session = session
	return nil
}

func producerCommand(line string) (string, []string) {
	fields := strings.Fields(line)
	index := 0
	if index < len(fields) && strings.HasPrefix(fields[index], "@") {
		index++
	}
	if index < len(fields) && strings.HasPrefix(fields[index], ":") {
		index++
	}
	if index >= len(fields) {
		return "", nil
	}
	return fields[index], fields[index+1:]
}

func validateProducerWire(message twitch.OutboundMessage) error {
	if !utf8.ValidString(message.Text) || strings.TrimSpace(message.Text) == "" {
		return errors.New("IRC message requires nonempty valid UTF-8")
	}
	limit := twitch.MaxChatMessageRunes
	if message.Action {
		limit -= 9
	}
	if utf8.RuneCountInString(message.Text) > limit {
		return errors.New("IRC message exceeds framed character limit")
	}
	for _, character := range message.Text {
		if unicode.IsControl(character) {
			return errors.New("IRC message contains forbidden control")
		}
	}
	if !utf8.ValidString(message.ReplyToMessageID) || len(message.ReplyToMessageID) > 512 || strings.ContainsAny(message.ReplyToMessageID, " ;\\\t\r\n") {
		return errors.New("invalid IRC reply identifier")
	}
	for _, character := range message.ReplyToMessageID {
		if unicode.IsControl(character) {
			return errors.New("invalid IRC reply identifier")
		}
	}
	return nil
}
