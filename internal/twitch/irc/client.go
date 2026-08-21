package irc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gempir "github.com/gempir/go-twitch-irc/v4"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/debuglog"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	defaultEventBuffer   = 128
	defaultOAuthTokenURL = "https://id.twitch.tv/oauth2/token"
	// oauthRefreshTimeout bounds the token-endpoint call made while
	// recovering a dropped connection, matching the login flow's timeout.
	oauthRefreshTimeout = 15 * time.Second
	// maxAuthRefreshes bounds how many times one connect attempt will
	// refresh and retry, so a server that keeps rejecting a freshly minted
	// token cannot spin.
	maxAuthRefreshes = 3
)

var oauthTokenPattern = regexp.MustCompile(`(?i)oauth:[^\s]+`)

// Config contains the credentials and channels needed to open a Twitch IRC
// session. OAuthToken must be an IRC token with the oauth: prefix.
type Config struct {
	Username     string
	OAuthToken   string
	RefreshToken string
	ClientID     string
	ClientSecret string
	TokenURL     string
	HTTPClient   *http.Client
	Channels     []string
	Buffer       int
	Now          func() time.Time
	DebugLogger  debuglog.Logger
	// OnOAuthRefresh is called after a successful auth refresh and before the
	// replacement IRC session connects. Callback errors are reported as
	// redacted warnings and do not prevent reconnect with the refreshed token.
	OnOAuthRefresh func(context.Context, OAuthRefresh) error
}

// OAuthRefresh describes refreshed Twitch OAuth credentials produced while
// recovering from an IRC authentication failure.
type OAuthRefresh struct {
	AccessToken         auth.Secret
	RefreshToken        auth.Secret
	TokenType           string
	Scopes              []auth.Scope
	ExpiresAt           time.Time
	RefreshedAt         time.Time
	RefreshTokenUpdated bool
}

// Redactor returns an auth redactor configured with refreshed token material.
func (r OAuthRefresh) Redactor() auth.Redactor {
	return auth.NewRedactor(r.AccessToken, r.RefreshToken)
}

type chatSession interface {
	Connect() error
	Disconnect() error
	Join(channels ...string)
	Depart(channel string)
	Say(channel, text string)
	Reply(channel, parentMsgID, text string)
	OnConnect(func())
	OnPrivateMessage(func(gempir.PrivateMessage))
	OnNoticeMessage(func(gempir.NoticeMessage))
	OnUserNoticeMessage(func(gempir.UserNoticeMessage))
	OnRoomStateMessage(func(gempir.RoomStateMessage))
	OnClearChatMessage(func(gempir.ClearChatMessage))
	OnClearMessage(func(gempir.ClearMessage))
	OnUserStateMessage(func(gempir.UserStateMessage))
	OnUserJoinMessage(func(gempir.UserJoinMessage))
	OnUserPartMessage(func(gempir.UserPartMessage))
	OnReconnectMessage(func(gempir.ReconnectMessage))
	OnUnsetMessage(func(gempir.RawMessage))
}

// Client adapts go-twitch-irc callbacks into twi's normalized event stream.
type Client struct {
	client         chatSession
	username       string
	token          string
	channels       []string
	buffer         int
	now            func() time.Time
	refresh        oauthRefreshConfig
	logger         debuglog.Logger
	onOAuthRefresh func(context.Context, OAuthRefresh) error
	mu             sync.RWMutex

	done      chan struct{}
	closeOnce sync.Once

	limiter *sendLimiter

	droppedEvents atomic.Uint64
	// connected tracks whether the IRC session has completed registration.
	// go-twitch-irc's Say/Reply queue a line and return nothing, so this is
	// the only way to tell a send that reached Twitch from one written into a
	// buffer on a dead socket.
	connected atomic.Bool
}

var (
	_ twitch.ChatClient       = (*Client)(nil)
	_ twitch.ChannelJoiner    = (*Client)(nil)
	_ twitch.EventDropCounter = (*Client)(nil)
)

// DroppedEvents implements EventDropCounter: the number of events discarded
// because the consumer could not keep up.
func (c *Client) DroppedEvents() uint64 {
	return c.droppedEvents.Load()
}

// NewClient creates a Twitch IRC client without opening the network
// connection. Call Connect to start the read loop.
func NewClient(cfg Config) (*Client, error) {
	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		return nil, errors.New("missing Twitch username")
	}
	token := strings.TrimSpace(cfg.OAuthToken)
	if token == "" {
		return nil, errors.New("missing Twitch OAuth token")
	}

	// Zero channels is a supported start: `twi chat` without
	// --channel/--channels connects to Twitch and waits for the /channels
	// picker to join the first one.
	channels := normalizeChannels(cfg.Channels)

	buffer := cfg.Buffer
	if buffer <= 0 {
		buffer = defaultEventBuffer
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	client := newSession(username, token, channels)
	logger := cfg.DebugLogger.WithSecrets(
		auth.NewSecret(token),
		auth.NewSecret(cfg.RefreshToken),
		auth.NewSecret(cfg.ClientSecret),
	)

	return &Client{
		client:   client,
		username: username,
		token:    token,
		channels: channels,
		buffer:   buffer,
		now:      now,
		refresh: oauthRefreshConfig{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RefreshToken: cfg.RefreshToken,
			TokenURL:     cfg.TokenURL,
			HTTPClient:   cfg.HTTPClient,
			Logger:       logger,
		},
		logger:         logger,
		onOAuthRefresh: cfg.OnOAuthRefresh,
		limiter:        newSendLimiter(now),
		done:           make(chan struct{}),
	}, nil
}

func (c *Client) Connect(ctx context.Context) (<-chan twitch.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	events := make(chan twitch.Event, c.buffer)
	c.logger.Log(ctx, "twitch.irc.connect.start",
		slog.Int("channel_count", len(c.channels)),
		slog.Int("buffer", c.buffer),
	)
	// emit drops the oldest queued event rather than blocking when the
	// consumer falls behind. This function runs on go-twitch-irc's parser
	// goroutine, which is also the goroutine that answers PING: blocking here
	// stops keepalives and Twitch closes the connection. Losing the oldest
	// unread event is a far smaller failure than losing the session.
	emit := func(event twitch.Event) {
		// Track registration state on the way past: Send has no other way to
		// know whether the socket is live.
		if event.Kind == twitch.EventConnection {
			switch event.Connection.Type {
			case twitch.ConnectionEventConnect:
				c.connected.Store(true)
			case twitch.ConnectionEventDisconnect:
				c.connected.Store(false)
			}
		}
		select {
		case events <- event:
			return
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
		}

		select {
		case <-events:
			c.droppedEvents.Add(1)
		default:
		}
		select {
		case events <- event:
		case <-ctx.Done():
		case <-c.done:
		default:
			c.droppedEvents.Add(1)
		}
	}

	client := c.currentClient()

	registerHandlers(client, emit, c.now)

	go func() {
		defer close(events)

		err := c.connectWithAuthRefresh(ctx, emit, client)
		if err == nil || errors.Is(err, gempir.ErrClientDisconnected) {
			c.logger.Log(ctx, "twitch.irc.connect.closed", slog.Bool("clean", true))
			emit(NormalizeDisconnect(nil, c.now()))
			return
		}
		safeErr := credentialSafeError(err)
		c.logger.Log(ctx, "twitch.irc.connect.failed", slog.String("error", safeErr.Error()))
		emit(NormalizeDisconnect(safeErr, c.now()))
	}()

	return events, nil
}

func registerHandlers(client chatSession, emit func(twitch.Event), now func() time.Time) {
	client.OnConnect(func() {
		emit(NormalizeConnect(now()))
	})
	client.OnPrivateMessage(func(message gempir.PrivateMessage) {
		emit(NormalizePrivateMessage(message))
	})
	client.OnNoticeMessage(func(message gempir.NoticeMessage) {
		emit(NormalizeNoticeMessage(message))
	})
	client.OnUserNoticeMessage(func(message gempir.UserNoticeMessage) {
		emit(NormalizeUserNoticeMessage(message))
	})
	client.OnRoomStateMessage(func(message gempir.RoomStateMessage) {
		emit(NormalizeRoomStateMessage(message))
	})
	client.OnClearChatMessage(func(message gempir.ClearChatMessage) {
		emit(NormalizeClearChatMessage(message))
	})
	client.OnClearMessage(func(message gempir.ClearMessage) {
		emit(NormalizeClearMessage(message))
	})
	client.OnUserStateMessage(func(message gempir.UserStateMessage) {
		emit(NormalizeUserStateMessage(message))
	})
	client.OnUserJoinMessage(func(message gempir.UserJoinMessage) {
		emit(NormalizeUserJoinMessage(message, now()))
	})
	client.OnUserPartMessage(func(message gempir.UserPartMessage) {
		emit(NormalizeUserPartMessage(message, now()))
	})
	client.OnReconnectMessage(func(message gempir.ReconnectMessage) {
		emit(NormalizeReconnectMessage(message, now()))
	})
	client.OnUnsetMessage(func(message gempir.RawMessage) {
		emit(NormalizeRawMessage(message))
	})
}

// connectWithAuthRefresh connects, refreshing the access token and retrying
// when Twitch rejects it.
//
// It loops rather than refreshing once. A session can outlive several token
// expiries -- an eight-hour stream crosses at least one -- and the previous
// single shot ended in a bare return, so the second expiry disconnected chat
// with no way back except restarting twi. The attempt count is bounded so a
// server that keeps rejecting freshly minted tokens cannot spin.
func (c *Client) connectWithAuthRefresh(ctx context.Context, emit func(twitch.Event), client chatSession) error {
	for attempt := range maxAuthRefreshes {
		// Stop before connecting anything if the client has been closed.
		//
		// A refresh installs a replacement session and then loops back here
		// to connect it. A Close landing in that window disconnects the
		// replacement -- which is not connected yet, so it reports "not
		// open" and is treated as success -- and then this loop would open a
		// fresh TCP and TLS session that nothing holds a reference to and
		// nothing can ever close. Returning nil reports a clean shutdown,
		// which is what a Close is.
		select {
		case <-c.done:
			return nil
		default:
		}

		err := c.connectOnceWithAuthRefresh(ctx, emit, client)
		if !errors.Is(err, errAuthRetryable) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.logger.Log(ctx, "twitch.irc.auth_refresh.retry", slog.Int("attempt", attempt+1))
		client = c.currentClient()
	}
	// Joined to ErrAuthFailed like every other credential rejection: having
	// exhausted the refresh attempts, the credentials are the problem, and
	// the app should say so rather than report a generic connection failure.
	return fmt.Errorf("twitch IRC authentication kept failing after refreshing the access token: %w", twitch.ErrAuthFailed)
}

// errAuthRetryable signals connectWithAuthRefresh that a refresh succeeded and
// the replacement session is ready to be connected.
var errAuthRetryable = errors.New("twitch IRC auth refreshed; retry connect")

func (c *Client) connectOnceWithAuthRefresh(ctx context.Context, emit func(twitch.Event), client chatSession) error {
	err := client.Connect()
	if !errors.Is(err, gempir.ErrLoginAuthenticationFailed) || !c.refresh.available() {
		return err
	}

	emit(twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{
		Type:   twitch.ConnectionEventReconnect,
		At:     c.now(),
		Reason: "Twitch IRC authentication failed; refreshing access token",
	}})
	c.logger.Log(ctx, "twitch.irc.auth_refresh.start")

	refreshedAt := c.now().UTC()
	refreshed, refreshErr := c.refresh.refresh(ctx, refreshedAt)
	if refreshErr != nil {
		c.logger.Log(ctx, "twitch.irc.auth_refresh.failed", slog.String("error", redactError(refreshErr.Error())))
		return fmt.Errorf("refresh Twitch OAuth token after IRC auth failure: %w", refreshErr)
	}
	c.logger.Log(ctx, "twitch.irc.auth_refresh.succeeded", slog.Bool("refresh_token_updated", refreshed.RefreshTokenUpdated))

	oldToken := c.token
	oldRefreshToken := c.refresh.RefreshToken
	token := refreshed.AccessToken.Reveal()
	refreshToken := refreshed.RefreshToken.Reveal()
	c.mu.Lock()
	c.token = token
	c.refresh.RefreshToken = refreshToken
	next := newSession(c.username, token, c.channels)
	c.client = next
	c.mu.Unlock()

	if err := c.persistOAuthRefresh(ctx, refreshed, oldToken, oldRefreshToken, emit); err != nil {
		c.logger.Log(ctx, "twitch.irc.auth_refresh.persistence_failed", slog.String("error", err.Error()))
	}

	registerHandlers(next, emit, c.now)
	// Hand the replacement session back to the loop rather than connecting it
	// here, so a later expiry can refresh again instead of ending the session.
	return errAuthRetryable
}

func (c *Client) Send(ctx context.Context, channel, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel = normalizeChannel(channel)
	c.logger.Log(ctx, "twitch.irc.send",
		slog.String("channel", channel),
		slog.Int("text_length", len([]rune(text))),
	)
	if channel == "" {
		return errors.New("missing Twitch channel")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("message text cannot be empty")
	}
	if err := c.requireConnected(); err != nil {
		return err
	}
	wire := sanitizeText(text)
	if err := c.limiter.allow(channel, wire); err != nil {
		return err
	}
	c.currentClient().Say(channel, wire)
	return nil
}

// requireConnected refuses a send on a session that is not registered.
//
// go-twitch-irc's Say and Reply return nothing: they queue a line onto an
// internal channel whether or not a connection exists. Without this check,
// Send always returned nil, so the composer reported a message as accepted
// while it was written into a buffer on a dead socket and never reached
// Twitch. Reporting success for something that did not happen is worse than
// reporting the failure.
func (c *Client) requireConnected() error {
	if c.connected.Load() {
		return nil
	}
	return twitch.ErrNotConnected
}

func (c *Client) Reply(ctx context.Context, channel, parentMessageID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel = normalizeChannel(channel)
	c.logger.Log(ctx, "twitch.irc.reply",
		slog.String("channel", channel),
		slog.String("reply_to_message_id", parentMessageID),
		slog.Int("text_length", len([]rune(text))),
	)
	if channel == "" {
		return errors.New("missing Twitch channel")
	}
	if strings.TrimSpace(parentMessageID) == "" {
		return errors.New("missing parent message ID")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("message text cannot be empty")
	}
	if err := c.requireConnected(); err != nil {
		return err
	}
	wire := sanitizeText(text)
	// Replies count against the same allowance, but a reply to a different
	// message is not a duplicate even with identical text, so the parent ID
	// is part of the key.
	if err := c.limiter.allow(channel+"\x00"+parentMessageID, wire); err != nil {
		return err
	}
	c.currentClient().Reply(channel, parentMessageID, wire)
	return nil
}

// Join subscribes to additional channels on the live connection. The channel
// list is also recorded so a reconnect rejoins everything currently open,
// not just the channels configured at startup.
func (c *Client) Join(channels ...string) error {
	normalized := normalizeChannels(channels)
	if len(normalized) == 0 {
		return errors.New("missing Twitch channel")
	}
	c.mu.Lock()
	for _, channel := range normalized {
		if !containsChannel(c.channels, channel) {
			c.channels = append(c.channels, channel)
		}
	}
	c.mu.Unlock()
	c.currentClient().Join(normalized...)
	c.logger.Log(context.Background(), "twitch.irc.join", slog.Int("channel_count", len(normalized)))
	return nil
}

// Depart leaves a channel and drops it from the reconnect list.
func (c *Client) Depart(channel string) error {
	normalized := normalizeChannels([]string{channel})
	if len(normalized) == 0 {
		return errors.New("missing Twitch channel")
	}
	target := normalized[0]
	c.mu.Lock()
	remaining := make([]string, 0, len(c.channels))
	for _, existing := range c.channels {
		if existing != target {
			remaining = append(remaining, existing)
		}
	}
	c.channels = remaining
	c.mu.Unlock()
	c.currentClient().Depart(target)
	c.logger.Log(context.Background(), "twitch.irc.depart", slog.String("channel", target))
	return nil
}

func containsChannel(channels []string, channel string) bool {
	for _, existing := range channels {
		if existing == channel {
			return true
		}
	}
	return false
}

func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.currentClient().Disconnect()
		if errors.Is(err, gempir.ErrConnectionIsNotOpen) {
			err = nil
		}
	})
	return err
}

func (c *Client) currentClient() chatSession {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

var newSession = func(username, token string, channels []string) chatSession {
	client := gempir.NewClient(username, token)
	// Tags carry badges, colors, emote spans, and reply metadata; commands
	// carry moderation and room-state events; membership carries the
	// JOIN/PART lines the chatter roster is built from. Membership is
	// best-effort on Twitch's side - it is batched, delayed, and dropped
	// entirely for large channels - but without requesting it here twi
	// receives none at all.
	client.Capabilities = []string{
		gempir.TagsCapability,
		gempir.CommandsCapability,
		gempir.MembershipCapability,
	}
	client.Join(channels...)
	return client
}

func (c *Client) persistOAuthRefresh(ctx context.Context, refreshed OAuthRefresh, oldToken, oldRefreshToken string, emit func(twitch.Event)) error {
	if c.onOAuthRefresh == nil {
		return nil
	}
	err := c.onOAuthRefresh(ctx, refreshed)
	if err == nil {
		c.logger.Log(ctx, "twitch.irc.auth_refresh.persistence_succeeded")
		return nil
	}
	warning := c.refreshPersistenceWarning(err, refreshed, oldToken, oldRefreshToken)
	emit(twitch.Event{Kind: twitch.EventConnection, Connection: twitch.ConnectionEvent{
		Type:   twitch.ConnectionEventReconnect,
		At:     c.now(),
		Reason: warning,
	}})
	return errors.New(warning)
}

func (c *Client) refreshPersistenceWarning(err error, refreshed OAuthRefresh, oldToken, oldRefreshToken string) string {
	redactor := auth.NewRedactor(
		auth.NewSecret(oldToken),
		auth.NewSecret(oldRefreshToken),
		auth.NewSecret(c.token),
		auth.NewSecret(c.refresh.RefreshToken),
		auth.NewSecret(c.refresh.ClientSecret),
		refreshed.AccessToken,
		refreshed.RefreshToken,
	)
	detail := redactError(redactor.Redact(err.Error()))
	return "warning: Twitch IRC refreshed OAuth credentials but could not save them (" + detail + "); using refreshed credentials in memory for this chat session only. Run `twi login` on a supported Unix platform or update env/config credentials before the next session."
}

func credentialSafeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gempir.ErrLoginAuthenticationFailed) {
		// Joined to ErrAuthFailed so the app can classify this without
		// inspecting the message, and to the original cause so nothing
		// further up loses context it might need.
		return twitch.NewSafeError(
			"twitch IRC authentication failed; verify username, OAuth token, and chat:read scope",
			errors.Join(twitch.ErrAuthFailed, err),
		)
	}
	return twitch.NewSafeError("twitch IRC connection failed: "+redactError(err.Error()), err)
}

func redactError(value string) string {
	value = auth.NewRedactor().Redact(value)
	return oauthTokenPattern.ReplaceAllString(value, "oauth:<redacted>")
}

func normalizeChannels(values []string) []string {
	channels := make([]string, 0, len(values))
	for _, value := range values {
		channel := normalizeChannel(value)
		if channel != "" {
			channels = append(channels, channel)
		}
	}
	return channels
}

// normalizeChannel turns a channel name in any of the forms a user or a
// config file might supply it into the form Twitch IRC expects.
//
// The trims run outermost-first: whitespace, then the optional "#" prefix.
// The other order silently fails on a value with leading whitespace -- " #foo"
// keeps its "#", because TrimPrefix sees a space -- and the result then never
// matches the same channel as recorded by the UI.
//
// The lowercasing is a wire requirement, not a formatting choice: IRC channel
// names are case-insensitive, so "#Foo" and "#foo" are one channel. Do not
// reuse this for anything the user sees, which should keep their capitals.
func normalizeChannel(value string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "#"))
}

type oauthRefreshConfig struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	TokenURL     string
	HTTPClient   *http.Client
	Logger       debuglog.Logger
}

type oauthRefreshResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	Scopes       []string `json:"scope"`
}

func (c oauthRefreshConfig) available() bool {
	return strings.TrimSpace(c.ClientID) != "" &&
		strings.TrimSpace(c.ClientSecret) != "" &&
		strings.TrimSpace(c.RefreshToken) != ""
}

func (c oauthRefreshConfig) refresh(ctx context.Context, refreshedAt time.Time) (OAuthRefresh, error) {
	endpoint := strings.TrimSpace(c.TokenURL)
	if endpoint == "" {
		endpoint = defaultOAuthTokenURL
	}
	fields := debuglog.URLFields("token_url", endpoint)
	c.Logger.Log(ctx, "twitch.oauth_refresh.request", fields...)
	httpClient := c.HTTPClient
	if httpClient == nil {
		// Not http.DefaultClient: it has no timeout, and nothing else on this
		// path sets a deadline either, so a token endpoint that accepts the
		// connection and then stalls would hang the reconnect indefinitely --
		// with chat already down. Matches the login flow's timeout.
		httpClient = &http.Client{Timeout: oauthRefreshTimeout}
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", strings.TrimSpace(c.RefreshToken))
	form.Set("client_id", strings.TrimSpace(c.ClientID))
	form.Set("client_secret", strings.TrimSpace(c.ClientSecret))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthRefresh{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		c.Logger.Log(ctx, "twitch.oauth_refresh.failed", slog.String("error", redactError(err.Error())))
		return OAuthRefresh{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.Logger.Log(ctx, "twitch.oauth_refresh.failed", slog.Int("http_status", resp.StatusCode))
		return OAuthRefresh{}, fmt.Errorf("twitch OAuth refresh returned HTTP %d", resp.StatusCode)
	}

	var decoded oauthRefreshResponse
	if err := decodeJSONBody(resp.Body, maxOAuthRefreshBodySize, &decoded); err != nil {
		c.Logger.Log(ctx, "twitch.oauth_refresh.failed", slog.String("error", redactError(err.Error())))
		return OAuthRefresh{}, err
	}

	accessToken := normalizeOAuthToken(decoded.AccessToken)
	if accessToken == "" {
		c.Logger.Log(ctx, "twitch.oauth_refresh.failed", slog.String("error", "missing access token"))
		return OAuthRefresh{}, errors.New("twitch OAuth refresh response did not include an access token")
	}

	refreshToken := strings.TrimSpace(decoded.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(c.RefreshToken)
	}
	c.Logger.Log(ctx, "twitch.oauth_refresh.succeeded", slog.Bool("refresh_token_returned", strings.TrimSpace(decoded.RefreshToken) != ""))
	if refreshedAt.IsZero() {
		refreshedAt = time.Now().UTC()
	}
	result := OAuthRefresh{
		AccessToken:         auth.NewSecret(accessToken),
		RefreshToken:        auth.NewSecret(refreshToken),
		TokenType:           strings.TrimSpace(decoded.TokenType),
		Scopes:              auth.Scopes(decoded.Scopes...),
		RefreshedAt:         refreshedAt,
		RefreshTokenUpdated: strings.TrimSpace(decoded.RefreshToken) != "" && refreshToken != strings.TrimSpace(c.RefreshToken),
	}
	if decoded.ExpiresIn > 0 {
		result.ExpiresAt = refreshedAt.Add(time.Duration(decoded.ExpiresIn) * time.Second)
	}
	return result, nil
}

func normalizeOAuthToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(strings.ToLower(value), "oauth:") {
		return value
	}
	return "oauth:" + value
}
