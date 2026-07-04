package twitch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	irc "github.com/gempir/go-twitch-irc/v4"
	"github.com/w0rxbend/twi/internal/auth"
	"github.com/w0rxbend/twi/internal/debuglog"
)

func TestNewIRCClientValidatesRequiredConfigWithoutLeakingToken(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  IRCConfig
		want string
	}{
		{
			name: "username",
			cfg: IRCConfig{
				OAuthToken: "oauth:secret-token",
				Channels:   []string{"example"},
			},
			want: "username",
		},
		{
			name: "token",
			cfg: IRCConfig{
				Username: "viewer",
				Channels: []string{"example"},
			},
			want: "OAuth token",
		},
		{
			name: "channel",
			cfg: IRCConfig{
				Username:   "viewer",
				OAuthToken: "oauth:secret-token",
			},
			want: "channel",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewIRCClient(tt.cfg)
			if err == nil {
				t.Fatal("NewIRCClient returned nil error, want validation error")
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

func TestNewIRCClientNormalizesChannelsAndCapabilities(t *testing.T) {
	client, err := NewIRCClient(IRCConfig{
		Username:   "viewer",
		OAuthToken: "oauth:secret-token",
		Channels:   []string{"#Example", " second "},
	})
	if err != nil {
		t.Fatalf("NewIRCClient returned error: %v", err)
	}
	if got, want := client.channels, []string{"example", "second"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("channels = %#v, want %#v", got, want)
	}
}

func TestCredentialSafeIRCErrorRedactsOAuthPattern(t *testing.T) {
	err := credentialSafeIRCError(errors.New("server rejected oauth:secret-token client_secret=client-secret"))
	if err == nil {
		t.Fatal("credentialSafeIRCError returned nil, want error")
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
	client, err := NewIRCClient(IRCConfig{
		Username:     "viewer",
		OAuthToken:   "oauth:configured-token",
		RefreshToken: "refresh-secret",
		ClientSecret: "client-secret",
		Channels:     []string{"example"},
		DebugLogger:  logger,
	})
	if err != nil {
		t.Fatalf("NewIRCClient returned error: %v", err)
	}

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
	oldNewIRCClient := newIRCClient
	t.Cleanup(func() {
		newIRCClient = oldNewIRCClient
	})

	var (
		mu       sync.Mutex
		sessions []*fakeIRCSession
	)
	newIRCClient = func(username, token string, channels []string) ircSession {
		mu.Lock()
		defer mu.Unlock()
		session := &fakeIRCSession{
			username: username,
			token:    token,
			channels: append([]string(nil), channels...),
		}
		if len(sessions) == 0 {
			session.connectErr = irc.ErrLoginAuthenticationFailed
		}
		sessions = append(sessions, session)
		return session
	}

	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return responseWithStatus(http.StatusOK, `{"access_token":"new-access-token","refresh_token":"new-refresh-token","token_type":"bearer"}`), nil
	})}

	var persisted []OAuthRefresh
	client, err := NewIRCClient(IRCConfig{
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
		t.Fatalf("NewIRCClient returned error: %v", err)
	}

	events, err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	var gotEvents []Event
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
		if event.Kind == EventConnection && event.Connection.Type == ConnectionEventReconnect && strings.Contains(event.Connection.Reason, "could not save") {
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

	mu          sync.Mutex
	connectErr  error
	connects    int
	disconnects int
	onConnect   func()
	says        []string
	replies     []string
}

var _ ircSession = (*fakeIRCSession)(nil)

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

func (s *fakeIRCSession) OnPrivateMessage(func(irc.PrivateMessage))       {}
func (s *fakeIRCSession) OnNoticeMessage(func(irc.NoticeMessage))         {}
func (s *fakeIRCSession) OnUserNoticeMessage(func(irc.UserNoticeMessage)) {}
func (s *fakeIRCSession) OnRoomStateMessage(func(irc.RoomStateMessage))   {}
func (s *fakeIRCSession) OnClearChatMessage(func(irc.ClearChatMessage))   {}
func (s *fakeIRCSession) OnClearMessage(func(irc.ClearMessage))           {}
func (s *fakeIRCSession) OnUserStateMessage(func(irc.UserStateMessage))   {}
func (s *fakeIRCSession) OnReconnectMessage(func(irc.ReconnectMessage))   {}
func (s *fakeIRCSession) OnUnsetMessage(func(irc.RawMessage))             {}

func (s *fakeIRCSession) connectCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connects
}
