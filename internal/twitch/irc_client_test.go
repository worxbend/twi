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
	"testing"

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
		return responseWithStatus(http.StatusOK, `{"access_token":"new-access-token","refresh_token":"new-refresh-token","token_type":"bearer"}`), nil
	})}

	token, refresh, err := oauthRefreshConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RefreshToken: "old-refresh-token",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
	}.refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh returned error: %v", err)
	}

	if token != "oauth:new-access-token" {
		t.Fatalf("token = %q, want oauth-prefixed token", token)
	}
	if refresh != "new-refresh-token" {
		t.Fatalf("refresh = %q, want new refresh token", refresh)
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

	token, refresh, err := oauthRefreshConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RefreshToken: "old-refresh-token",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
	}.refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh returned error: %v", err)
	}

	if token != "oauth:new-access-token" {
		t.Fatalf("token = %q, want existing oauth-prefixed token", token)
	}
	if refresh != "old-refresh-token" {
		t.Fatalf("refresh = %q, want existing refresh token", refresh)
	}
}

func TestOAuthRefreshConfigErrorsDoNotLeakSecrets(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return responseWithStatus(http.StatusUnauthorized, "secret-token-value"), nil
	})}

	_, _, err := oauthRefreshConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret-value",
		RefreshToken: "secret-token-value",
		TokenURL:     "https://example.invalid/token",
		HTTPClient:   httpClient,
	}.refresh(context.Background())
	if err == nil {
		t.Fatal("refresh returned nil error, want HTTP error")
	}
	for _, secret := range []string{"client-secret-value", "secret-token-value"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("refresh error leaked secret %q: %v", secret, err)
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
