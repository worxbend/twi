package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	irc "github.com/gempir/go-twitch-irc/v4"
	"github.com/w0rxbend/twi/internal/auth"
	"github.com/w0rxbend/twi/internal/debuglog"
)

const (
	defaultIRCEventBuffer = 128
	defaultOAuthTokenURL  = "https://id.twitch.tv/oauth2/token"
)

var ircOAuthTokenPattern = regexp.MustCompile(`(?i)oauth:[^\s]+`)

// IRCConfig contains the credentials and channels needed to open a Twitch IRC
// session. OAuthToken must be an IRC token with the oauth: prefix.
type IRCConfig struct {
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

type ircSession interface {
	Connect() error
	Disconnect() error
	Say(channel, text string)
	Reply(channel, parentMsgID, text string)
	OnConnect(func())
	OnPrivateMessage(func(irc.PrivateMessage))
	OnNoticeMessage(func(irc.NoticeMessage))
	OnUserNoticeMessage(func(irc.UserNoticeMessage))
	OnRoomStateMessage(func(irc.RoomStateMessage))
	OnClearChatMessage(func(irc.ClearChatMessage))
	OnClearMessage(func(irc.ClearMessage))
	OnUserStateMessage(func(irc.UserStateMessage))
	OnReconnectMessage(func(irc.ReconnectMessage))
	OnUnsetMessage(func(irc.RawMessage))
}

// IRCClient adapts go-twitch-irc callbacks into twi's normalized event stream.
type IRCClient struct {
	client         ircSession
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
}

var _ ChatClient = (*IRCClient)(nil)

// NewIRCClient creates a Twitch IRC client without opening the network
// connection. Call Connect to start the read loop.
func NewIRCClient(cfg IRCConfig) (*IRCClient, error) {
	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		return nil, errors.New("missing Twitch username")
	}
	token := strings.TrimSpace(cfg.OAuthToken)
	if token == "" {
		return nil, errors.New("missing Twitch OAuth token")
	}

	channels := normalizeIRCChannels(cfg.Channels)
	if len(channels) == 0 {
		return nil, errors.New("missing Twitch channel")
	}

	buffer := cfg.Buffer
	if buffer <= 0 {
		buffer = defaultIRCEventBuffer
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	client := newIRCClient(username, token, channels)
	logger := cfg.DebugLogger.WithSecrets(
		auth.NewSecret(token),
		auth.NewSecret(cfg.RefreshToken),
		auth.NewSecret(cfg.ClientSecret),
	)

	return &IRCClient{
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
		done:           make(chan struct{}),
	}, nil
}

func (c *IRCClient) Connect(ctx context.Context) (<-chan Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	events := make(chan Event, c.buffer)
	c.logger.Log(ctx, "twitch.irc.connect.start",
		slog.Int("channel_count", len(c.channels)),
		slog.Int("buffer", c.buffer),
	)
	emit := func(event Event) {
		select {
		case events <- event:
		case <-ctx.Done():
		case <-c.done:
		}
	}

	client := c.currentClient()

	registerIRCHandlers(client, emit, c.now)

	go func() {
		defer close(events)

		err := c.connectWithAuthRefresh(ctx, emit, client)
		if err == nil || errors.Is(err, irc.ErrClientDisconnected) {
			c.logger.Log(ctx, "twitch.irc.connect.closed", slog.Bool("clean", true))
			emit(NormalizeIRCDisconnect(nil, c.now()))
			return
		}
		safeErr := credentialSafeIRCError(err)
		c.logger.Log(ctx, "twitch.irc.connect.failed", slog.String("error", safeErr.Error()))
		emit(NormalizeIRCDisconnect(safeErr, c.now()))
	}()

	return events, nil
}

func registerIRCHandlers(client ircSession, emit func(Event), now func() time.Time) {
	client.OnConnect(func() {
		emit(NormalizeIRCConnect(now()))
	})
	client.OnPrivateMessage(func(message irc.PrivateMessage) {
		emit(NormalizeIRCPrivateMessage(message))
	})
	client.OnNoticeMessage(func(message irc.NoticeMessage) {
		emit(NormalizeIRCNoticeMessage(message))
	})
	client.OnUserNoticeMessage(func(message irc.UserNoticeMessage) {
		emit(NormalizeIRCUserNoticeMessage(message))
	})
	client.OnRoomStateMessage(func(message irc.RoomStateMessage) {
		emit(NormalizeIRCRoomStateMessage(message))
	})
	client.OnClearChatMessage(func(message irc.ClearChatMessage) {
		emit(NormalizeIRCClearChatMessage(message))
	})
	client.OnClearMessage(func(message irc.ClearMessage) {
		emit(NormalizeIRCClearMessage(message))
	})
	client.OnUserStateMessage(func(message irc.UserStateMessage) {
		emit(NormalizeIRCUserStateMessage(message))
	})
	client.OnReconnectMessage(func(message irc.ReconnectMessage) {
		emit(NormalizeIRCReconnectMessage(message, now()))
	})
	client.OnUnsetMessage(func(message irc.RawMessage) {
		emit(NormalizeIRCRawMessage(message))
	})
}

func (c *IRCClient) connectWithAuthRefresh(ctx context.Context, emit func(Event), client ircSession) error {
	err := client.Connect()
	if !errors.Is(err, irc.ErrLoginAuthenticationFailed) || !c.refresh.available() {
		return err
	}

	emit(Event{Kind: EventConnection, Connection: ConnectionEvent{
		Type:   ConnectionEventReconnect,
		At:     c.now(),
		Reason: "Twitch IRC authentication failed; refreshing access token",
	}})
	c.logger.Log(ctx, "twitch.irc.auth_refresh.start")

	refreshedAt := c.now().UTC()
	refreshed, refreshErr := c.refresh.refresh(ctx, refreshedAt)
	if refreshErr != nil {
		c.logger.Log(ctx, "twitch.irc.auth_refresh.failed", slog.String("error", redactIRCError(refreshErr.Error())))
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
	next := newIRCClient(c.username, token, c.channels)
	c.client = next
	c.mu.Unlock()

	if err := c.persistOAuthRefresh(ctx, refreshed, oldToken, oldRefreshToken, emit); err != nil {
		c.logger.Log(ctx, "twitch.irc.auth_refresh.persistence_failed", slog.String("error", err.Error()))
	}

	registerIRCHandlers(next, emit, c.now)
	return next.Connect()
}

func (c *IRCClient) Send(ctx context.Context, channel, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel = normalizeIRCChannel(channel)
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
	c.currentClient().Say(channel, text)
	return nil
}

func (c *IRCClient) Reply(ctx context.Context, channel, parentMessageID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel = normalizeIRCChannel(channel)
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
	c.currentClient().Reply(channel, parentMessageID, text)
	return nil
}

func (c *IRCClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.currentClient().Disconnect()
		if errors.Is(err, irc.ErrConnectionIsNotOpen) {
			err = nil
		}
	})
	return err
}

func (c *IRCClient) currentClient() ircSession {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

var newIRCClient = func(username, token string, channels []string) ircSession {
	client := irc.NewClient(username, token)
	client.Capabilities = []string{irc.TagsCapability, irc.CommandsCapability}
	client.Join(channels...)
	return client
}

func (c *IRCClient) persistOAuthRefresh(ctx context.Context, refreshed OAuthRefresh, oldToken, oldRefreshToken string, emit func(Event)) error {
	if c.onOAuthRefresh == nil {
		return nil
	}
	err := c.onOAuthRefresh(ctx, refreshed)
	if err == nil {
		c.logger.Log(ctx, "twitch.irc.auth_refresh.persistence_succeeded")
		return nil
	}
	warning := c.refreshPersistenceWarning(err, refreshed, oldToken, oldRefreshToken)
	emit(Event{Kind: EventConnection, Connection: ConnectionEvent{
		Type:   ConnectionEventReconnect,
		At:     c.now(),
		Reason: warning,
	}})
	return errors.New(warning)
}

func (c *IRCClient) refreshPersistenceWarning(err error, refreshed OAuthRefresh, oldToken, oldRefreshToken string) string {
	redactor := auth.NewRedactor(
		auth.NewSecret(oldToken),
		auth.NewSecret(oldRefreshToken),
		auth.NewSecret(c.token),
		auth.NewSecret(c.refresh.RefreshToken),
		auth.NewSecret(c.refresh.ClientSecret),
		refreshed.AccessToken,
		refreshed.RefreshToken,
	)
	detail := redactIRCError(redactor.Redact(err.Error()))
	return "warning: Twitch IRC refreshed OAuth credentials but could not save them (" + detail + "); using refreshed credentials in memory for this chat session only. Run `twi login` on a supported platform or update env/config credentials before the next session."
}

func credentialSafeIRCError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, irc.ErrLoginAuthenticationFailed) {
		return errors.New("twitch IRC authentication failed; verify username, OAuth token, and chat:read scope")
	}
	return fmt.Errorf("twitch IRC connection failed: %s", redactIRCError(err.Error()))
}

func redactIRCError(value string) string {
	value = auth.NewRedactor().Redact(value)
	return ircOAuthTokenPattern.ReplaceAllString(value, "oauth:<redacted>")
}

func normalizeIRCChannels(values []string) []string {
	channels := make([]string, 0, len(values))
	for _, value := range values {
		channel := normalizeIRCChannel(value)
		if channel != "" {
			channels = append(channels, channel)
		}
	}
	return channels
}

func normalizeIRCChannel(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "#")))
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
		httpClient = http.DefaultClient
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
		c.Logger.Log(ctx, "twitch.oauth_refresh.failed", slog.String("error", redactIRCError(err.Error())))
		return OAuthRefresh{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.Logger.Log(ctx, "twitch.oauth_refresh.failed", slog.Int("http_status", resp.StatusCode))
		return OAuthRefresh{}, fmt.Errorf("twitch OAuth refresh returned HTTP %d", resp.StatusCode)
	}

	var decoded oauthRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		c.Logger.Log(ctx, "twitch.oauth_refresh.failed", slog.String("error", redactIRCError(err.Error())))
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
