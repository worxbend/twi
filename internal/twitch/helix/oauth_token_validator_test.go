package helix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

func TestOAuthTokenValidatorSuccess(t *testing.T) {
	now := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth access-token" {
			t.Fatalf("authorization header = %q, want OAuth access-token", got)
		}
		fmt.Fprint(w, `{"client_id":"client-id","login":"viewer","scopes":["chat:read","chat:edit"],"user_id":"42","expires_in":3600}`)
	}))
	defer server.Close()

	validator := NewOAuthTokenValidator(OAuthTokenValidatorConfig{
		Endpoint: server.URL,
		Now:      func() time.Time { return now },
	})

	result, err := validator.ValidateToken(context.Background(), twitch.TokenCredentials{
		Username:     "Viewer",
		OAuthToken:   "oauth:access-token",
		RefreshToken: "refresh-secret",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	})
	if err != nil {
		t.Fatalf("ValidateToken error = %v", err)
	}
	if result.Status != twitch.TokenValidationValid {
		t.Fatalf("status = %q, want valid; detail=%q", result.Status, result.Detail)
	}
	if result.Identity != (twitch.TokenIdentity{UserID: "42", Login: "viewer"}) {
		t.Fatalf("identity = %#v, want viewer identity", result.Identity)
	}
	if !reflect.DeepEqual(result.Scopes, []twitch.TokenScope{twitch.ScopeChatRead, twitch.ScopeChatEdit}) {
		t.Fatalf("scopes = %#v, want IRC scopes", result.Scopes)
	}
	if len(result.MissingScopes) != 0 {
		t.Fatalf("missing scopes = %#v, want none", result.MissingScopes)
	}
	if !result.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expires at = %s, want %s", result.ExpiresAt, now.Add(time.Hour))
	}
	if !result.RefreshAvailable {
		t.Fatal("refresh available = false, want true")
	}
}

func TestOAuthTokenValidatorMapsCredentialStates(t *testing.T) {
	for _, tt := range []struct {
		name         string
		username     string
		body         string
		wantStatus   twitch.TokenValidationStatus
		wantMissing  []twitch.TokenScope
		wantDetail   string
		wantIdentity twitch.TokenIdentity
	}{
		{
			name:        "missing scopes",
			username:    "viewer",
			body:        `{"client_id":"client-id","login":"viewer","scopes":["chat:read"],"user_id":"42","expires_in":3600}`,
			wantStatus:  twitch.TokenValidationMissingScope,
			wantMissing: []twitch.TokenScope{twitch.ScopeChatEdit},
		},
		{
			name:       "username mismatch",
			username:   "viewer",
			body:       `{"client_id":"client-id","login":"other_viewer","scopes":["chat:read","chat:edit"],"user_id":"43","expires_in":3600}`,
			wantStatus: twitch.TokenValidationWrongUser,
			wantDetail: "other_viewer",
			wantIdentity: twitch.TokenIdentity{
				UserID: "43",
				Login:  "other_viewer",
			},
		},
		{
			name:       "expired",
			username:   "viewer",
			body:       `{"client_id":"client-id","login":"viewer","scopes":["chat:read","chat:edit"],"user_id":"42","expires_in":0}`,
			wantStatus: twitch.TokenValidationExpired,
			wantDetail: "expired",
		},
		{
			name:       "missing identity is malformed",
			username:   "viewer",
			body:       `{"client_id":"client-id","scopes":["chat:read","chat:edit"],"expires_in":3600}`,
			wantStatus: twitch.TokenValidationMalformed,
			wantDetail: "identity",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			validator := NewOAuthTokenValidator(OAuthTokenValidatorConfig{Endpoint: server.URL})
			result, err := validator.ValidateToken(context.Background(), twitch.TokenCredentials{
				Username:   tt.username,
				OAuthToken: "oauth:access-token",
			})
			if err != nil {
				t.Fatalf("ValidateToken error = %v", err)
			}
			if result.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q; result=%#v", result.Status, tt.wantStatus, result)
			}
			if tt.wantMissing != nil && !reflect.DeepEqual(result.MissingScopes, tt.wantMissing) {
				t.Fatalf("missing scopes = %#v, want %#v", result.MissingScopes, tt.wantMissing)
			}
			if tt.wantDetail != "" && !strings.Contains(result.Detail, tt.wantDetail) {
				t.Fatalf("detail = %q, want it to contain %q", result.Detail, tt.wantDetail)
			}
			if tt.wantIdentity != (twitch.TokenIdentity{}) && result.Identity != tt.wantIdentity {
				t.Fatalf("identity = %#v, want %#v", result.Identity, tt.wantIdentity)
			}
		})
	}
}

func TestOAuthTokenValidatorMapsTwitchUnauthorizedErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"status":401,"message":"invalid access token"}`)
	}))
	defer server.Close()

	validator := NewOAuthTokenValidator(OAuthTokenValidatorConfig{Endpoint: server.URL})
	result, err := validator.ValidateToken(context.Background(), twitch.TokenCredentials{
		OAuthToken: "oauth:access-token",
	})
	if err != nil {
		t.Fatalf("ValidateToken error = %v", err)
	}
	if result.Status != twitch.TokenValidationMalformed {
		t.Fatalf("status = %q, want malformed", result.Status)
	}
	if !strings.Contains(result.Detail, "invalid access token") {
		t.Fatalf("detail = %q, want Twitch error message", result.Detail)
	}
}

func TestOAuthTokenValidatorRedactsAdapterErrors(t *testing.T) {
	credentials := twitch.TokenCredentials{
		OAuthToken:   "oauth:access-token",
		RefreshToken: "refresh-secret",
		ClientSecret: "client-secret",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, strings.Join([]string{
			"oauth:access-token",
			"Bearer bearer-secret",
			"client_secret=client-secret",
			"client-secret",
			"refresh_token=refresh-secret",
			"refresh-secret",
			"authorization_code=auth-code-secret",
		}, " "))
	}))
	defer server.Close()

	validator := NewOAuthTokenValidator(OAuthTokenValidatorConfig{Endpoint: server.URL})
	_, err := validator.ValidateToken(context.Background(), credentials)
	if err == nil {
		t.Fatal("ValidateToken error = nil, want HTTP error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "Bearer bearer-secret", "bearer-secret", "client-secret", "refresh-secret", "auth-code-secret")
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("error = %q, want redaction marker", err.Error())
	}
}

func TestOAuthTokenValidatorMalformedResponseIsCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not-json oauth:access-token client_secret=client-secret refresh_token=refresh-secret authorization_code=auth-code-secret`)
	}))
	defer server.Close()

	validator := NewOAuthTokenValidator(OAuthTokenValidatorConfig{Endpoint: server.URL})
	_, err := validator.ValidateToken(context.Background(), twitch.TokenCredentials{
		OAuthToken:   "oauth:access-token",
		RefreshToken: "refresh-secret",
		ClientSecret: "client-secret",
	})
	if err == nil {
		t.Fatal("ValidateToken error = nil, want decode error")
	}
	assertTokenValidatorErrorDoesNotLeak(t, err, "oauth:access-token", "access-token", "client-secret", "refresh-secret", "auth-code-secret")
}

func TestOAuthTokenValidatorHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()

	validator := NewOAuthTokenValidator(OAuthTokenValidatorConfig{Endpoint: server.URL})
	_, err := validator.ValidateToken(ctx, twitch.TokenCredentials{OAuthToken: "oauth:access-token"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateToken error = %v, want context.Canceled", err)
	}
}

func TestOAuthTokenValidatorPreservesCancellationWhileReadingErrorBody(t *testing.T) {
	validator := NewOAuthTokenValidator(OAuthTokenValidatorConfig{
		Endpoint: "https://example.test/oauth2/validate",
		HTTPClient: &http.Client{Transport: oauthValidatorRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       errorReadCloser{err: context.Canceled},
				Header:     make(http.Header),
			}, nil
		})},
	})

	_, err := validator.ValidateToken(context.Background(), twitch.TokenCredentials{OAuthToken: "oauth:access-token"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateToken error = %v, want context.Canceled", err)
	}
}

func TestOAuthTokenValidatorDoesNotRequireLiveCredentials(t *testing.T) {
	validator := NewOAuthTokenValidator(OAuthTokenValidatorConfig{})
	result, err := validator.ValidateToken(context.Background(), twitch.TokenCredentials{})
	if err != nil {
		t.Fatalf("ValidateToken error = %v", err)
	}
	if result.Status != twitch.TokenValidationMalformed {
		t.Fatalf("status = %q, want malformed for missing token", result.Status)
	}
}

type oauthValidatorRoundTripFunc func(*http.Request) (*http.Response, error)

func (f oauthValidatorRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errorReadCloser struct {
	err error
}

func (r errorReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r errorReadCloser) Close() error {
	return nil
}

func assertTokenValidatorErrorDoesNotLeak(t *testing.T, err error, secrets ...string) {
	t.Helper()
	text := err.Error()
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("error leaked %q: %s", secret, text)
		}
	}
}
