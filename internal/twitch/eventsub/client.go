// Package eventsub adapts Twitch EventSub WebSocket chat notifications.
package eventsub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/jsonbody"
)

type Channel struct{ Login, ID string }
type Config struct {
	Scopes            []auth.Scope
	ModeratorChannels []string
	ClientID, UserID  string
	AccountLogin      string
	Token             func() string
	Channels          []Channel
	ExcludedChannels  []string
	URL, APIURL       string
	HTTPClient        *http.Client
	Buffer            int
	ConnectTimeout    time.Duration
}
type Client struct {
	sockets  map[*websocket.Conn]struct{}
	closed   bool
	config   Config
	cancel   context.CancelFunc
	mu       sync.Mutex
	started  bool
	terminal error
	lease    *sendquota.ChannelLease
	dropped  atomic.Uint64
}

func NewClient(config Config) (*Client, error) {
	if config.AccountLogin == "" || config.ClientID == "" || config.UserID == "" || config.Token == nil || len(config.Channels) == 0 || len(config.Channels) > MaxReadChannels {
		return nil, errors.New("EventSub requires client, user, token and 1 to 42 channels")
	}
	for _, channel := range config.Channels {
		if channel.Login == "" || channel.ID == "" {
			return nil, errors.New("EventSub channels require resolved identity")
		}
	}
	if config.URL == "" {
		config.URL = "wss://eventsub.wss.twitch.tv/ws"
	}
	if config.APIURL == "" {
		config.APIURL = "https://api.twitch.tv/helix/eventsub/subscriptions"
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if config.Buffer <= 0 {
		config.Buffer = 128
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 30 * time.Second
	}
	config.Buffer = max(config.Buffer, len(config.Channels)+2)
	config.Scopes = append([]auth.Scope(nil), config.Scopes...)
	config.ModeratorChannels = append([]string(nil), config.ModeratorChannels...)
	config.Channels = append([]Channel(nil), config.Channels...)
	config.ExcludedChannels = append([]string(nil), config.ExcludedChannels...)
	candidate := &Client{config: config, sockets: make(map[*websocket.Conn]struct{})}
	count := 0
	for _, channel := range config.Channels {
		count += len(candidate.subscriptions(channel))
	}
	if count > 300 {
		return nil, errors.New("EventSub subscription count exceeds 300")
	}
	return candidate, nil
}
func (c *Client) DroppedEvents() uint64 { return c.dropped.Load() }

func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	cancel := c.cancel
	sockets := make([]*websocket.Conn, 0, len(c.sockets))
	for socket := range c.sockets {
		sockets = append(sockets, socket)
	}
	lease := c.lease
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, socket := range sockets {
		_ = socket.CloseNow()
	}
	if lease != nil {
		return lease.Close()
	}
	return nil
}

type envelope struct {
	Metadata struct {
		ID           string    `json:"message_id"`
		Type         string    `json:"message_type"`
		At           time.Time `json:"message_timestamp"`
		Subscription string    `json:"subscription_type"`
	} `json:"metadata"`
	Payload struct {
		Session struct {
			ID        string `json:"id"`
			Keepalive int    `json:"keepalive_timeout_seconds"`
			Reconnect string `json:"reconnect_url"`
		} `json:"session"`
		Subscription struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"subscription"`
		Event json.RawMessage `json:"event"`
	} `json:"payload"`
}

func (c *Client) welcome(ctx context.Context, endpoint string) (*websocket.Conn, envelope, error) {
	bounded, cancel := context.WithTimeout(ctx, c.config.ConnectTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(bounded, endpoint, &websocket.DialOptions{HTTPClient: c.config.HTTPClient})
	if err != nil {
		return nil, envelope{}, errors.New("EventSub WebSocket connection failed")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.CloseNow()
		return nil, envelope{}, context.Canceled
	}
	c.sockets[conn] = struct{}{}
	c.mu.Unlock()
	conn.SetReadLimit(1 << 20)
	_, data, err := conn.Read(bounded)
	var frame envelope
	if err == nil {
		err = json.Unmarshal(data, &frame)
	}
	if err != nil || frame.Metadata.Type != "session_welcome" || frame.Payload.Session.ID == "" {
		_ = conn.CloseNow()
		c.mu.Lock()
		delete(c.sockets, conn)
		c.mu.Unlock()
		return nil, envelope{}, errors.New("EventSub welcome missing or invalid")
	}
	return conn, frame, nil
}
func (c *Client) subscribe(ctx context.Context, session string, channel Channel, kind string) error {
	if kind == "channel.chat.message" {
		if err := sendquota.AcquireConnection(ctx, strings.ToLower(c.config.AccountLogin), "joins", 1); err != nil {
			return err
		}
	}
	request := struct {
		Type      string            `json:"type"`
		Version   string            `json:"version"`
		Condition map[string]string `json:"condition"`
		Transport map[string]string `json:"transport"`
	}{kind, "1", map[string]string{"broadcaster_user_id": channel.ID, "user_id": c.config.UserID}, map[string]string{"method": "websocket", "session_id": session}}
	if kind == "channel.moderate" {
		request.Version = "2"
		delete(request.Condition, "user_id")
		request.Condition["moderator_user_id"] = c.config.UserID
	}
	body, _ := json.Marshal(request)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.APIURL, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid EventSub subscription endpoint")
	}
	req.Header.Set("Client-Id", c.config.ClientID)
	req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(c.config.Token(), "oauth:"))
	req.Header.Set("Content-Type", "application/json")
	response, err := c.config.HTTPClient.Do(req)
	if err != nil {
		return errors.New("EventSub subscription request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return fmt.Errorf("EventSub authorization denied: %w", twitch.ErrAuthFailed)
		}
		return fmt.Errorf("EventSub subscription returned HTTP %d", response.StatusCode)
	}
	var result struct {
		TotalCost int `json:"total_cost"`
		MaxCost   int `json:"max_total_cost"`
	}
	if err = jsonbody.Decode(response.Body, 1<<20, &result); err != nil {
		return errors.New("invalid EventSub subscription response")
	}
	if result.MaxCost > 0 && result.TotalCost > result.MaxCost {
		return errors.New("EventSub subscription quota exhausted")
	}
	return nil
}
func (c *Client) Connect(parent context.Context) (<-chan twitch.Event, error) {
	c.mu.Lock()
	if c.started || c.closed {
		c.mu.Unlock()
		return nil, errors.New("EventSub Connect called more than once")
	}
	c.started = true
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.mu.Unlock()
	channels := make([]string, 0, len(c.config.Channels))
	for _, channel := range c.config.Channels {
		channels = append(channels, channel.Login)
	}
	lease, err := sendquota.ReserveChannels(ctx, strings.ToLower(c.config.AccountLogin), channels)
	if err != nil {
		cancel()
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = lease.Close()
		return nil, context.Canceled
	}
	c.lease = lease
	c.mu.Unlock()
	conn, welcome, err := c.welcome(ctx, c.config.URL)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	startup, finish := context.WithTimeout(ctx, c.config.ConnectTimeout)
	defer finish()
	firstSubscription := true
	for _, channel := range c.config.Channels {
		for _, kind := range c.subscriptions(channel) {
			subscriptionCtx := startup
			finishFirst := func() {}
			if firstSubscription {
				subscriptionCtx, finishFirst = context.WithTimeout(startup, 10*time.Second)
				firstSubscription = false
			}
			err = c.subscribe(subscriptionCtx, welcome.Payload.Session.ID, channel, kind)
			finishFirst()
			if err != nil {
				_ = conn.CloseNow()
				_ = c.Close()
				return nil, err
			}
		}
	}
	events := make(chan twitch.Event, c.config.Buffer)
	emit := func(event twitch.Event) {
		if event.Kind == twitch.EventError {
			c.setTerminal(event.Err)
		}
		select {
		case events <- event:
		case <-ctx.Done():
		default:
			c.dropped.Add(1)
		}
	}
	go c.run(ctx, conn, welcome, events, emit)
	return events, nil
}

type readResult struct {
	conn  *websocket.Conn
	frame envelope
	err   error
}

func (c *Client) run(ctx context.Context, conn *websocket.Conn, welcome envelope, events chan twitch.Event, emit func(twitch.Event)) {
	defer close(events)
	defer c.Close()
	defer func() { _ = conn.CloseNow() }()
	for _, channel := range c.config.Channels {
		emit(twitch.Event{Kind: twitch.EventChannelReady, RoomState: twitch.RoomState{Channel: channel.Login, RoomID: channel.ID}})
	}
	results := make(chan readResult, 8)
	startReader := func(socket *websocket.Conn, keepalive int) {
		go func() {
			for {
				readCtx, cancel := context.WithTimeout(ctx, time.Duration(max(keepalive, 10)+1)*time.Second)
				_, data, err := socket.Read(readCtx)
				cancel()
				var frame envelope
				if err == nil {
					err = json.Unmarshal(data, &frame)
				}
				select {
				case results <- readResult{socket, frame, err}:
				case <-ctx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}()
	}
	startReader(conn, welcome.Payload.Session.Keepalive)
	handover := make(chan readResult)
	var switching bool
	seen := make(map[string]bool)
	order := make([]string, 0, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-c.lease.Errors():
			emit(twitch.Event{Kind: twitch.EventError, Err: err})
			return
		case replacement := <-handover:
			switching = false
			if replacement.err != nil {
				emit(twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventDisconnect, At: time.Now().UTC(), Reason: "EventSub handover failed"}})
				return
			}
			old := conn
			conn = replacement.conn
			startReader(conn, replacement.frame.Payload.Session.Keepalive)
			_ = old.CloseNow()
			c.mu.Lock()
			delete(c.sockets, old)
			c.mu.Unlock()
		case result := <-results:
			if result.conn != conn && (result.err != nil || result.frame.Metadata.Type != "notification") {
				continue
			}
			if result.err != nil {
				emit(twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{Type: twitch.ConnectionEventDisconnect, At: time.Now().UTC(), Reason: "EventSub connection lost"}})
				return
			}
			frame := result.frame
			switch frame.Metadata.Type {
			case "session_keepalive":
			case "session_reconnect":
				if switching {
					continue
				}
				endpoint, err := url.Parse(frame.Payload.Session.Reconnect)
				base, _ := url.Parse(c.config.URL)
				if err != nil || endpoint == nil || endpoint.Host != base.Host || endpoint.Scheme != base.Scheme {
					emit(twitch.Event{Kind: twitch.EventError, Err: errors.New("invalid EventSub reconnect endpoint")})
					return
				}
				switching = true
				go func() {
					socket, first, err := c.welcome(ctx, endpoint.String())
					select {
					case handover <- readResult{socket, first, err}:
					case <-ctx.Done():
						if socket != nil {
							_ = socket.CloseNow()
						}
					}
				}()
			case "revocation":
				emit(twitch.Event{Kind: twitch.EventError, Err: fmt.Errorf("EventSub subscription revoked (%s): %w", frame.Payload.Subscription.Status, twitch.ErrAuthFailed)})
				return
			case "notification":
				if seen[frame.Metadata.ID] {
					continue
				}
				seen[frame.Metadata.ID] = true
				order = append(order, frame.Metadata.ID)
				if len(order) > 4096 {
					delete(seen, order[0])
					order = order[1:]
				}
				event, err := normalize(frame)
				if err != nil {
					emit(twitch.Event{Kind: twitch.EventError, Err: err})
					return
				}
				emit(event)
			}
		}
	}
}

// TerminalError preserves fatal capability/protocol failures even under overload.
func (c *Client) TerminalError() error  { c.mu.Lock(); defer c.mu.Unlock(); return c.terminal }
func (c *Client) setTerminal(err error) { c.mu.Lock(); c.terminal = err; c.mu.Unlock() }

// UnavailableChannels reports selected identities absent from the lookup result.
func (c *Client) UnavailableChannels() []string {
	return append([]string(nil), c.config.ExcludedChannels...)
}
