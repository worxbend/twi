package irc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gempir "github.com/gempir/go-twitch-irc/v4"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/debuglog"
	"github.com/worxbend/twi/internal/twitch"
)

func TestNewIRCClientValidatesRequiredConfigWithoutLeakingToken(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "username",
			cfg: Config{
				OAuthToken: "oauth:secret-token",
				Channels:   []string{"example"},
			},
			want: "username",
		},
		{
			name: "token",
			cfg: Config{
				Username: "viewer",
				Channels: []string{"example"},
			},
			want: "OAuth token",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.cfg)
			if err == nil {
				t.Fatal("NewClient returned nil error, want validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), tt.want)
			}
			if strings.Contains(err.Error(), "oauth:secret-token") {
				t.Fatalf("error leaked token: %q", err.Error())
			}
		})
	}
}

// Starting with no channel is supported: `twi chat` without
// --channel/--channels connects and waits for the /channels picker to join
// the first one.
func TestNewIRCClientAllowsNoChannels(t *testing.T) {
	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:secret-token",
	})
	if err != nil {
		t.Fatalf("NewClient error = %v, want nil for a channel-less start", err)
	}
	if len(client.channels) != 0 {
		t.Fatalf("channels = %#v, want none", client.channels)
	}
}

// Join and Depart also maintain the reconnect channel list, so a reconnect
// rejoins what is open now rather than what was configured at startup.
func TestIRCClientJoinAndDepartTrackChannels(t *testing.T) {
	session := &fakeIRCSession{}
	restore := newSession
	newSession = func(username, token string, channels []string) chatSession {
		session.username, session.token, session.channels = username, token, channels
		return session
	}
	defer func() { newSession = restore }()

	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:secret-token",
		Channels:   []string{"alpha"},
	})
	if err != nil {
		t.Fatalf("NewClient error = %v", err)
	}
	if err := client.Join("#Beta"); err != nil {
		t.Fatalf("Join error = %v", err)
	}
	if got := client.channels; len(got) != 2 || got[1] != "beta" {
		t.Fatalf("channels after join = %#v, want [alpha beta]", got)
	}
	// Joining an open channel again must not duplicate it in the list.
	if err := client.Join("beta"); err != nil {
		t.Fatalf("second Join error = %v", err)
	}
	if got := client.channels; len(got) != 2 {
		t.Fatalf("channels after duplicate join = %#v, want 2 entries", got)
	}
	if err := client.Depart("alpha"); err != nil {
		t.Fatalf("Depart error = %v", err)
	}
	if got := client.channels; len(got) != 1 || got[0] != "beta" {
		t.Fatalf("channels after depart = %#v, want [beta]", got)
	}
	if got := session.departed; len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("session departed = %#v, want [alpha]", got)
	}
}

func TestNewIRCClientNormalizesChannelsAndCapabilities(t *testing.T) {
	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:secret-token",
		Channels:   []string{"#Example", " second "},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if got, want := client.channels, []string{"example", "second"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("channels = %#v, want %#v", got, want)
	}
}

func TestCredentialSafeIRCErrorRedactsOAuthPattern(t *testing.T) {
	err := credentialSafeError(errors.New("server rejected oauth:secret-token client_secret=client-secret"))
	if err == nil {
		t.Fatal("credentialSafeError returned nil, want error")
	}
	if strings.Contains(err.Error(), "oauth:secret-token") || strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("error leaked token: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("error = %q, want redacted token marker", err.Error())
	}
}

func TestIRCClientDebugLogsSendFieldsWithoutMessageTextOrCredentials(t *testing.T) {
	var logs bytes.Buffer
	logger := debuglog.New(&logs, debuglog.Options{Enabled: true})
	client, err := NewClient(Config{
		Username:     "viewer",
		OAuthToken:   "oauth:configured-token",
		RefreshToken: "refresh-secret",
		ClientSecret: "client-secret",
		Channels:     []string{"example"},
		DebugLogger:  logger,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	// Send refuses on an unregistered session, so mark this one connected:
	// the subject here is what the debug log records, not the gate.
	client.connected.Store(true)

	client.logger.Log(context.Background(), "twitch.irc.test_error", slog.String("error", "oauth:configured-token refresh-secret client-secret"))
	client.refresh.Logger.Log(context.Background(), "twitch.oauth_refresh.test_error", slog.String("error", "oauth:configured-token refresh-secret client-secret"))
	if err := client.Send(context.Background(), "example", "hello oauth:message-secret"); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if err := client.Reply(context.Background(), "example", "parent-1", "reply refresh_token=reply-secret"); err != nil {
		t.Fatalf("Reply returned error: %v", err)
	}

	output := logs.String()
	for _, want := range []string{`"event":"twitch.irc.send"`, `"event":"twitch.irc.reply"`, `"channel":"example"`, `"reply_to_message_id":"parent-1"`, `"text_length":`} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{
		"oauth:configured-token",
		"configured-token",
		"refresh-secret",
		"client-secret",
		"oauth:message-secret",
		"message-secret",
		"reply-secret",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("debug log leaked %q:\n%s", forbidden, output)
		}
	}
}

func TestOAuthRefreshConfigRefreshesAccessToken(t *testing.T) {
	var gotForm url.Values
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Fatalf("content-type = %q, want form encoding", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotForm, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		return responseWithStatus(http.StatusOK, `{"access_token":"new-access-token","refresh_token":"new-refresh-token","token_type":"bearer","expires_in":3600,"scope":["chat:read","chat:edit"]}`), nil
	})}

	refreshedAt := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	refreshed, err := oauthRefreshConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RefreshToken: "old-refresh-token",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
	}.refresh(context.Background(), refreshedAt)
	if err != nil {
		t.Fatalf("refresh returned error: %v", err)
	}

	if got := refreshed.AccessToken.Reveal(); got != "oauth:new-access-token" {
		t.Fatalf("token = %q, want oauth-prefixed token", got)
	}
	if got := refreshed.RefreshToken.Reveal(); got != "new-refresh-token" {
		t.Fatalf("refresh = %q, want new refresh token", got)
	}
	if !refreshed.RefreshTokenUpdated {
		t.Fatal("RefreshTokenUpdated = false, want true")
	}
	if refreshed.TokenType != "bearer" {
		t.Fatalf("TokenType = %q, want bearer", refreshed.TokenType)
	}
	if got, want := strings.Join(auth.ScopeValues(refreshed.Scopes), ","), "chat:read,chat:edit"; got != want {
		t.Fatalf("Scopes = %q, want %q", got, want)
	}
	if !refreshed.ExpiresAt.Equal(refreshedAt.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %s, want one hour after refresh", refreshed.ExpiresAt)
	}
	for key, want := range map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": "old-refresh-token",
		"client_id":     "client-id",
		"client_secret": "client-secret",
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s] = %q, want %q", key, got, want)
		}
	}
}

func TestOAuthRefreshConfigKeepsExistingRefreshTokenWhenResponseOmitsOne(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return responseWithStatus(http.StatusOK, `{"access_token":"oauth:new-access-token","token_type":"bearer"}`), nil
	})}

	refreshed, err := oauthRefreshConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RefreshToken: "old-refresh-token",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
	}.refresh(context.Background(), time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("refresh returned error: %v", err)
	}

	if got := refreshed.AccessToken.Reveal(); got != "oauth:new-access-token" {
		t.Fatalf("token = %q, want existing oauth-prefixed token", got)
	}
	if got := refreshed.RefreshToken.Reveal(); got != "old-refresh-token" {
		t.Fatalf("refresh = %q, want existing refresh token", got)
	}
	if refreshed.RefreshTokenUpdated {
		t.Fatal("RefreshTokenUpdated = true, want false when response omits refresh token")
	}
}

func TestOAuthRefreshConfigErrorsDoNotLeakSecrets(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return responseWithStatus(http.StatusUnauthorized, "secret-token-value"), nil
	})}

	_, err := oauthRefreshConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret-value",
		RefreshToken: "secret-token-value",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
	}.refresh(context.Background(), time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("refresh returned nil error, want HTTP error")
	}
	for _, secret := range []string{"client-secret-value", "secret-token-value"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("refresh error leaked secret %q: %v", secret, err)
		}
	}
}

func TestIRCClientAuthRefreshPersistenceFailureWarnsAndReconnectsWithRefreshedToken(t *testing.T) {
	oldNewIRCClient := newSession
	t.Cleanup(func() {
		newSession = oldNewIRCClient
	})

	var (
		mu       sync.Mutex
		sessions []*fakeIRCSession
	)
	newSession = func(username, token string, channels []string) chatSession {
		mu.Lock()
		defer mu.Unlock()
		session := &fakeIRCSession{
			username: username,
			token:    token,
			channels: append([]string(nil), channels...),
		}
		if len(sessions) == 0 {
			session.connectErr = gempir.ErrLoginAuthenticationFailed
		}
		sessions = append(sessions, session)
		return session
	}

	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return responseWithStatus(http.StatusOK, `{"access_token":"new-access-token","refresh_token":"new-refresh-token","token_type":"bearer"}`), nil
	})}

	var persisted []OAuthRefresh
	client, err := NewClient(Config{
		Username:     "viewer",
		OAuthToken:   "oauth:old-access-token",
		RefreshToken: "old-refresh-token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
		Channels:     []string{"example"},
		Now: func() time.Time {
			return time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
		},
		OnOAuthRefresh: func(_ context.Context, refreshed OAuthRefresh) error {
			persisted = append(persisted, refreshed)
			return errors.New("cannot write oauth:new-access-token refresh_token=new-refresh-token client_secret=client-secret")
		},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	events, err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	var gotEvents []twitch.Event
	for event := range events {
		gotEvents = append(gotEvents, event)
	}

	mu.Lock()
	gotSessions := append([]*fakeIRCSession(nil), sessions...)
	mu.Unlock()
	if len(gotSessions) != 2 {
		t.Fatalf("sessions = %d, want initial plus refreshed reconnect", len(gotSessions))
	}
	if gotSessions[1].token != "oauth:new-access-token" {
		t.Fatalf("replacement token = %q, want refreshed token", gotSessions[1].token)
	}
	if gotSessions[1].connectCalls() != 1 {
		t.Fatalf("replacement connect calls = %d, want 1", gotSessions[1].connectCalls())
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted refresh callbacks = %d, want 1", len(persisted))
	}
	if persisted[0].AccessToken.Reveal() != "oauth:new-access-token" || persisted[0].RefreshToken.Reveal() != "new-refresh-token" {
		t.Fatalf("persisted tokens = (%q, %q), want refreshed tokens", persisted[0].AccessToken.Reveal(), persisted[0].RefreshToken.Reveal())
	}

	warning := ""
	for _, event := range gotEvents {
		if event.Kind == twitch.EventConnection && event.Connection.Type == twitch.ConnectionEventReconnect && strings.Contains(event.Connection.Reason, "could not save") {
			warning = event.Connection.Reason
			break
		}
	}
	if warning == "" {
		t.Fatalf("events missing refresh persistence warning: %#v", gotEvents)
	}
	for _, want := range []string{"warning:", "using refreshed credentials in memory", "twi login", "env/config"} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning missing %q: %q", want, warning)
		}
	}
	for _, forbidden := range []string{"old-access-token", "new-access-token", "old-refresh-token", "new-refresh-token", "client-secret"} {
		if strings.Contains(warning, forbidden) {
			t.Fatalf("warning leaked %q: %q", forbidden, warning)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func responseWithStatus(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

type fakeIRCSession struct {
	username string
	token    string
	channels []string

	mu               sync.Mutex
	connectErr       error
	connects         int
	disconnects      int
	onConnect        func()
	onPrivateMessage func(gempir.PrivateMessage)
	says             []string
	replies          []string
	joined           []string
	departed         []string
}

var _ chatSession = (*fakeIRCSession)(nil)

func (s *fakeIRCSession) Join(channels ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.joined = append(s.joined, channels...)
}

func (s *fakeIRCSession) Depart(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.departed = append(s.departed, channel)
}

func (s *fakeIRCSession) Connect() error {
	s.mu.Lock()
	s.connects++
	err := s.connectErr
	onConnect := s.onConnect
	s.mu.Unlock()
	if err == nil && onConnect != nil {
		onConnect()
	}
	return err
}

func (s *fakeIRCSession) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disconnects++
	return nil
}

func (s *fakeIRCSession) Say(channel, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.says = append(s.says, channel+"\x00"+text)
}

func (s *fakeIRCSession) Reply(channel, parentMsgID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, channel+"\x00"+parentMsgID+"\x00"+text)
}

func (s *fakeIRCSession) OnConnect(callback func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onConnect = callback
}

func (s *fakeIRCSession) OnPrivateMessage(callback func(gempir.PrivateMessage)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onPrivateMessage = callback
}

// deliverPrivateMessage drives the handler the client registered, standing in
// for the library delivering a line off the socket.
func (s *fakeIRCSession) deliverPrivateMessage(message gempir.PrivateMessage) {
	s.mu.Lock()
	callback := s.onPrivateMessage
	s.mu.Unlock()
	if callback != nil {
		callback(message)
	}
}
func (s *fakeIRCSession) OnNoticeMessage(func(gempir.NoticeMessage))         {}
func (s *fakeIRCSession) OnUserNoticeMessage(func(gempir.UserNoticeMessage)) {}
func (s *fakeIRCSession) OnRoomStateMessage(func(gempir.RoomStateMessage))   {}
func (s *fakeIRCSession) OnClearChatMessage(func(gempir.ClearChatMessage))   {}
func (s *fakeIRCSession) OnClearMessage(func(gempir.ClearMessage))           {}
func (s *fakeIRCSession) OnUserStateMessage(func(gempir.UserStateMessage))   {}
func (s *fakeIRCSession) OnUserJoinMessage(func(gempir.UserJoinMessage))     {}
func (s *fakeIRCSession) OnUserPartMessage(func(gempir.UserPartMessage))     {}
func (s *fakeIRCSession) OnReconnectMessage(func(gempir.ReconnectMessage))   {}
func (s *fakeIRCSession) OnUnsetMessage(func(gempir.RawMessage))             {}

func (s *fakeIRCSession) connectCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connects
}

// The roster's JOIN/PART tracking only works if twi actually asks Twitch for
// the membership capability. The library default includes it, but twi sets
// Capabilities explicitly, so an override that drops membership would
// silently disable chat presence.
func TestIRCClientRequestsMembershipCapability(t *testing.T) {
	session := newSession("user", "oauth:token", []string{"alpha"})
	real, ok := session.(*gempir.Client)
	if !ok {
		t.Fatalf("newSession returned %T, want *gempir.Client", session)
	}

	want := map[string]bool{
		gempir.TagsCapability:       false,
		gempir.CommandsCapability:   false,
		gempir.MembershipCapability: false,
	}
	for _, capability := range real.Capabilities {
		if _, known := want[capability]; known {
			want[capability] = true
		}
	}
	for capability, requested := range want {
		if !requested {
			t.Errorf("capability %q not requested; got %v", capability, real.Capabilities)
		}
	}
}

// TestIRCClientRefreshesMoreThanOnce covers a session that outlives two token
// expiries, which any long stream does. connectWithAuthRefresh used to end in
// a bare `return next.Connect()`, so exactly one refresh was possible per
// process: the second expiry disconnected chat with no way back except
// restarting twi.
func TestIRCClientRefreshesMoreThanOnce(t *testing.T) {
	oldNewIRCClient := newSession
	t.Cleanup(func() { newSession = oldNewIRCClient })

	var (
		mu       sync.Mutex
		sessions []*fakeIRCSession
	)
	newSession = func(username, token string, channels []string) chatSession {
		mu.Lock()
		defer mu.Unlock()
		session := &fakeIRCSession{
			username: username,
			token:    token,
			channels: append([]string(nil), channels...),
		}
		// The first two connects are rejected, so two refreshes are needed
		// before the session establishes.
		if len(sessions) < 2 {
			session.connectErr = gempir.ErrLoginAuthenticationFailed
		}
		sessions = append(sessions, session)
		return session
	}

	var issued int
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		issued++
		body := fmt.Sprintf(`{"access_token":"new-access-token-%d","refresh_token":"new-refresh-token-%d","token_type":"bearer"}`, issued, issued)
		mu.Unlock()
		return responseWithStatus(http.StatusOK, body), nil
	})}

	client, err := NewClient(Config{
		Username:     "viewer",
		OAuthToken:   "oauth:old-access-token",
		RefreshToken: "old-refresh-token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
		Channels:     []string{"example"},
		Now:          func() time.Time { return time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	events, err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	for range events {
	}

	mu.Lock()
	got := append([]*fakeIRCSession(nil), sessions...)
	refreshes := issued
	mu.Unlock()

	if refreshes < 2 {
		t.Fatalf("token refreshes = %d, want at least 2; the second expiry must not end the session", refreshes)
	}
	if len(got) != 3 {
		t.Fatalf("sessions = %d, want initial plus two refreshed reconnects", len(got))
	}
	if got[2].token != "oauth:new-access-token-2" {
		t.Fatalf("final token = %q, want the second refreshed token", got[2].token)
	}
}

// TestIRCClientStopsRefreshingAfterRepeatedRejection bounds the loop: a server
// that keeps rejecting freshly minted tokens must not spin forever.
func TestIRCClientStopsRefreshingAfterRepeatedRejection(t *testing.T) {
	oldNewIRCClient := newSession
	t.Cleanup(func() { newSession = oldNewIRCClient })

	var mu sync.Mutex
	attempts := 0
	newSession = func(username, token string, channels []string) chatSession {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		return &fakeIRCSession{
			username:   username,
			token:      token,
			channels:   append([]string(nil), channels...),
			connectErr: gempir.ErrLoginAuthenticationFailed,
		}
	}

	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return responseWithStatus(http.StatusOK, `{"access_token":"still-rejected","refresh_token":"r","token_type":"bearer"}`), nil
	})}

	client, err := NewClient(Config{
		Username:     "viewer",
		OAuthToken:   "oauth:old",
		RefreshToken: "old-refresh",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
		Channels:     []string{"example"},
		Now:          func() time.Time { return time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	events, err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	for range events {
	}

	mu.Lock()
	got := attempts
	mu.Unlock()
	if got > maxAuthRefreshes+1 {
		t.Fatalf("built %d sessions, want no more than %d; the refresh loop is unbounded", got, maxAuthRefreshes+1)
	}
}

// TestIRCClientSendRefusesWhenNotConnected is the regression for a send that
// lied. go-twitch-irc's Say and Reply return nothing and queue the line
// regardless of connection state, so Send always returned nil: the composer
// showed a message as accepted while it sat in a buffer on a dead socket and
// never reached Twitch.
func TestIRCClientSendRefusesWhenNotConnected(t *testing.T) {
	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:token",
		Channels:   []string{"example"},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if err := client.Send(context.Background(), "example", "hello"); !errors.Is(err, twitch.ErrNotConnected) {
		t.Fatalf("Send error = %v, want ErrNotConnected", err)
	}
	if err := client.Reply(context.Background(), "example", "parent-1", "hi"); !errors.Is(err, twitch.ErrNotConnected) {
		t.Fatalf("Reply error = %v, want ErrNotConnected", err)
	}
	if twitch.IsAuthError(client.Send(context.Background(), "example", "hello")) {
		t.Fatal("a disconnected send was classified as an auth failure")
	}
}

func TestIRCClientSendSucceedsOnceConnected(t *testing.T) {
	oldNewIRCClient := newSession
	t.Cleanup(func() { newSession = oldNewIRCClient })
	session := &fakeIRCSession{}
	newSession = func(username, token string, channels []string) chatSession {
		session.username = username
		session.token = token
		session.channels = append([]string(nil), channels...)
		return session
	}

	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:token",
		Channels:   []string{"example"},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	client.connected.Store(true)

	if err := client.Send(context.Background(), "example", "hello"); err != nil {
		t.Fatalf("Send on a connected session returned %v, want nil", err)
	}
}

// TestIRCClientSendRefusesAfterDisconnect covers the state transition, which
// is the case that actually bites: the session was live and then dropped.
func TestIRCClientSendRefusesAfterDisconnect(t *testing.T) {
	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:token",
		Channels:   []string{"example"},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	client.connected.Store(true)
	if err := client.Send(context.Background(), "example", "hello"); err != nil {
		t.Fatalf("Send while connected returned %v, want nil", err)
	}

	client.connected.Store(false)
	if err := client.Send(context.Background(), "example", "hello"); !errors.Is(err, twitch.ErrNotConnected) {
		t.Fatalf("Send after disconnect = %v, want ErrNotConnected", err)
	}
}

// TestOAuthRefreshStopsReadingOversizedResponseBody bounds the token refresh.
//
// twi runs unattended for hours and refreshes its token on a timer. Decoding
// straight from resp.Body reads as much as the JSON asks for with no ceiling,
// so a token endpoint (or anything sitting in front of it) answering with an
// endless body would have the process buffer all of it. The body here is far
// larger than the 4 KiB cap, and the refresh must fail rather than swallow it.
func TestOAuthRefreshStopsReadingOversizedResponseBody(t *testing.T) {
	var read atomic.Int64
	oversized := `{"access_token":"a","token_type":"bearer","expires_in":3600,"padding":"` +
		strings.Repeat("x", 8*maxOAuthRefreshBodySize) + `"}`

	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		resp := responseWithStatus(http.StatusOK, oversized)
		resp.Body = io.NopCloser(&countingReader{r: resp.Body, count: &read})
		return resp, nil
	})}

	_, err := oauthRefreshConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RefreshToken: "old-refresh-token",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
	}.refresh(context.Background(), time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("refresh accepted an oversized token response; the decode is unbounded")
	}
	if got := read.Load(); got > maxOAuthRefreshBodySize {
		t.Fatalf("read %d bytes of the response body, want no more than %d", got, maxOAuthRefreshBodySize)
	}
}

// countingReader records how many bytes were actually pulled from a response
// body, which is what "bounded" means here -- not merely that the decode
// failed, but that it stopped reading.
type countingReader struct {
	r     io.Reader
	count *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.count.Add(int64(n))
	return n, err
}

// TestNormalizeChannelTrimsWhitespaceBeforeTheHash is a regression test for a
// channel name with leading whitespace keeping its "#".
//
// The trims used to run innermost-first, so TrimPrefix inspected a string that
// still began with a space, found no "#", and left it in place. " #foo" then
// normalized to "#foo" while the UI recorded the same channel as "foo", and
// the two never matched again.
func TestNormalizeChannelTrimsWhitespaceBeforeTheHash(t *testing.T) {
	tests := map[string]string{
		"#Foo":     "foo",
		" #Foo":    "foo",
		"\t#foo\n": "foo",
		" Foo ":    "foo",
		"foo":      "foo",
		"":         "",
		"#":        "",
	}
	for in, want := range tests {
		if got := normalizeChannel(in); got != want {
			t.Errorf("normalizeChannel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestConnectLoopStopsOnceClosed is a regression test for a leaked connection.
//
// The auth-refresh loop installs a replacement session and then loops back to
// connect it. A Close landing in that window disconnects the replacement --
// which has not connected yet, so it reports "not open" and that is treated as
// success -- and the loop would then open a fresh TCP and TLS session that
// nothing holds a reference to and nothing can ever close. On a long stream
// crossing several token expiries that leaks a live connection to Twitch per
// occurrence.
//
// The loop now checks c.done before connecting anything.
func TestConnectLoopStopsOnceClosed(t *testing.T) {
	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:token",
		Channels:   []string{"example"},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	session := &fakeIRCSession{}
	close(client.done) // the client has been closed

	if err := client.connectWithAuthRefresh(context.Background(), func(twitch.Event) {}, session); err != nil {
		t.Fatalf("connectWithAuthRefresh on a closed client = %v, want nil (clean shutdown)", err)
	}
	if got := session.connectCalls(); got != 0 {
		t.Errorf("Connect called %d times after Close, want 0: a connection was opened that nothing can close", got)
	}
}

// TestEmitDropsOldestEventWhenTheConsumerFallsBehind covers the drop policy
// that keeps the connection alive under load.
//
// The underlying library answers Twitch's PING from the same goroutine that
// delivers messages. If emit ever blocked on a full channel, that goroutine
// would stall, the PING would go unanswered, and Twitch would close the
// connection -- so a busy channel with a slow consumer would drop the session
// rather than a few lines. emit therefore discards the oldest buffered event
// to make room, and counts what it discarded so the UI can say so.
//
// Neither half of that was covered: the drop, nor the counter.
func TestEmitDropsOldestEventWhenTheConsumerFallsBehind(t *testing.T) {
	oldNewSession := newSession
	t.Cleanup(func() { newSession = oldNewSession })
	session := &fakeIRCSession{}
	newSession = func(string, string, []string) chatSession { return session }

	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:token",
		Channels:   []string{"example"},
		Buffer:     2,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	events, err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// Nothing reads events, so the buffer fills and then overflows.
	const sent = 8
	for i := range sent {
		session.deliverPrivateMessage(gempir.PrivateMessage{
			ID:      fmt.Sprintf("m%d", i),
			Channel: "example",
			Message: "hello",
			User:    gempir.User{Name: "chatter"},
		})
	}

	if got := client.DroppedEvents(); got == 0 {
		t.Fatal("DroppedEvents() = 0 after overflowing the buffer, want the discarded events counted")
	}
	// The counter must reflect the overflow rather than every event.
	if got := client.DroppedEvents(); got >= sent {
		t.Errorf("DroppedEvents() = %d, want fewer than the %d sent: buffered events must not be counted as dropped", got, sent)
	}
	// The newest event survives: emit discards the oldest to make room.
	drained := make([]twitch.Event, 0, 2)
	for len(drained) < 2 {
		select {
		case event := <-events:
			drained = append(drained, event)
		case <-time.After(time.Second):
			t.Fatalf("only drained %d events, want the buffer to still hold 2", len(drained))
		}
	}
	newest := drained[len(drained)-1]
	if newest.Message.ID != fmt.Sprintf("m%d", sent-1) {
		t.Errorf("newest buffered event = %q, want the most recent message m%d", newest.Message.ID, sent-1)
	}
}

// TestEmitDoesNotDropWhenTheConsumerKeepsUp is the negative case: a counter
// that always incremented would pass the test above.
func TestEmitDoesNotDropWhenTheConsumerKeepsUp(t *testing.T) {
	oldNewSession := newSession
	t.Cleanup(func() { newSession = oldNewSession })
	session := &fakeIRCSession{}
	newSession = func(string, string, []string) chatSession { return session }

	client, err := NewClient(Config{
		Username:   "viewer",
		OAuthToken: "oauth:token",
		Channels:   []string{"example"},
		Buffer:     8,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	events, err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	for i := range 4 {
		session.deliverPrivateMessage(gempir.PrivateMessage{
			ID:      fmt.Sprintf("m%d", i),
			Channel: "example",
			Message: "hello",
			User:    gempir.User{Name: "chatter"},
		})
		select {
		case <-events:
		case <-time.After(time.Second):
			t.Fatal("event was not delivered to a consumer that is keeping up")
		}
	}
	if got := client.DroppedEvents(); got != 0 {
		t.Errorf("DroppedEvents() = %d, want 0 when the consumer keeps up", got)
	}
}
