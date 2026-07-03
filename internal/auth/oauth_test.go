package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTwitchOAuthLoginFlowBeginBuildsAuthorizationURL(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: "https://id.example/oauth2/authorize",
		Now:               func() time.Time { return now },
		StateGenerator:    func() (Secret, error) { return NewSecret("state-secret"), nil },
	})

	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1:3000/callback",
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	authorizationURL, err := url.Parse(challenge.AuthorizationURL.Reveal())
	if err != nil {
		t.Fatalf("authorization URL parse error = %v", err)
	}
	query := authorizationURL.Query()
	if authorizationURL.Scheme != "https" || authorizationURL.Host != "id.example" || authorizationURL.Path != "/oauth2/authorize" {
		t.Fatalf("authorization URL = %q, want configured endpoint", challenge.AuthorizationURL.Reveal())
	}
	if got := query.Get("response_type"); got != "code" {
		t.Fatalf("response_type = %q, want code", got)
	}
	if got := query.Get("client_id"); got != "client-id" {
		t.Fatalf("client_id = %q, want client-id", got)
	}
	if got := query.Get("redirect_uri"); got != "http://127.0.0.1:3000/callback" {
		t.Fatalf("redirect_uri = %q, want callback URI", got)
	}
	if got := query.Get("scope"); got != "chat:read chat:edit" {
		t.Fatalf("scope = %q, want default chat scopes", got)
	}
	if got := query.Get("state"); got != "state-secret" {
		t.Fatalf("state = %q, want generated state", got)
	}
	if !reflect.DeepEqual(challenge.Scopes, RequiredChatScopes()) {
		t.Fatalf("challenge scopes = %#v, want default chat scopes", challenge.Scopes)
	}
	if !challenge.ExpiresAt.Equal(now.Add(defaultOAuthStateTTL)) {
		t.Fatalf("expires at = %s, want default state TTL", challenge.ExpiresAt)
	}

	formatted := fmt.Sprintf("%+v %#v", challenge, challenge)
	assertTextDoesNotLeak(t, formatted, "state-secret", challenge.AuthorizationURL.Reveal())
}

func TestTwitchOAuthLoginFlowCallbackSuccessExchangesValidatesAndCapturesRefreshToken(t *testing.T) {
	now := time.Date(2026, 7, 3, 13, 0, 0, 0, time.UTC)
	var tokenRequests int32
	var validateRequests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			atomic.AddInt32(&tokenRequests, 1)
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s, want POST", r.Method)
			}
			if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
				t.Fatalf("token content type = %q, want form", got)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm error = %v", err)
			}
			assertFormValue(t, r.Form, "grant_type", "authorization_code")
			assertFormValue(t, r.Form, "client_id", "client-id")
			assertFormValue(t, r.Form, "client_secret", "client-secret")
			assertFormValue(t, r.Form, "code", "callback-code")
			assertFormValue(t, r.Form, "redirect_uri", "http://127.0.0.1/callback")
			fmt.Fprint(w, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600,"scope":["chat:read","chat:edit"],"token_type":"bearer"}`)
		case "/validate":
			atomic.AddInt32(&validateRequests, 1)
			if r.Method != http.MethodGet {
				t.Fatalf("validate method = %s, want GET", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "OAuth access-token" {
				t.Fatalf("authorization header = %q, want OAuth access-token", got)
			}
			fmt.Fprint(w, `{"client_id":"client-id","login":"viewer","scopes":["chat:read","chat:edit"],"user_id":"42","expires_in":3590}`)
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: server.URL + "/authorize",
		TokenEndpoint:     server.URL + "/token",
		ValidateEndpoint:  server.URL + "/validate",
		Now:               func() time.Time { return now },
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		Scopes:       []Scope{ScopeChatRead, ScopeChatEdit},
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	callbackRequest := httptest.NewRequest(http.MethodGet, "/callback?code=callback-code&state=state-secret", nil)
	result, err := flow.CompleteLogin(context.Background(), LoginCallbackFromRequest(callbackRequest, challenge.State))
	if err != nil {
		t.Fatalf("CompleteLogin error = %v", err)
	}
	if result.Identity != (Identity{UserID: "42", Login: "viewer"}) {
		t.Fatalf("identity = %#v, want validated user", result.Identity)
	}
	if result.Tokens.AccessToken.Reveal() != "access-token" {
		t.Fatalf("access token was not captured")
	}
	if result.Tokens.RefreshToken.Reveal() != "refresh-token" {
		t.Fatalf("refresh token = %q, want captured refresh token", result.Tokens.RefreshToken.Reveal())
	}
	if !result.Tokens.RefreshAvailable() {
		t.Fatal("RefreshAvailable = false, want true")
	}
	if result.Tokens.TokenType != "bearer" {
		t.Fatalf("token type = %q, want bearer", result.Tokens.TokenType)
	}
	if !result.Tokens.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expires at = %s, want token response expiry", result.Tokens.ExpiresAt)
	}
	if !reflect.DeepEqual(result.Scopes, []Scope{ScopeChatRead, ScopeChatEdit}) {
		t.Fatalf("result scopes = %#v, want validated scopes", result.Scopes)
	}

	formatted := fmt.Sprintf("%+v %#v", result, result)
	assertTextDoesNotLeak(t, formatted, "access-token", "refresh-token")

	_, err = flow.CompleteLogin(context.Background(), LoginCallbackFromRequest(callbackRequest, challenge.State))
	if err == nil {
		t.Fatal("second CompleteLogin error = nil, want consumed state failure")
	}
	if !strings.Contains(err.Error(), "unknown or expired") {
		t.Fatalf("second CompleteLogin error = %q, want consumed state guidance", err.Error())
	}
	if got := atomic.LoadInt32(&tokenRequests); got != 1 {
		t.Fatalf("token requests = %d, want no second exchange", got)
	}
	if got := atomic.LoadInt32(&validateRequests); got != 1 {
		t.Fatalf("validate requests = %d, want no second validation", got)
	}
}

func TestTwitchOAuthLoginFlowRejectsInvalidStateBeforeHTTP(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		t.Fatalf("unexpected HTTP request for invalid state: %s", r.URL.Path)
	}))
	defer server.Close()

	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: server.URL + "/authorize",
		TokenEndpoint:     server.URL + "/token",
		ValidateEndpoint:  server.URL + "/validate",
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("wrong-state-secret"),
		ExpectedState: challenge.State,
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want invalid state error")
	}
	if !strings.Contains(err.Error(), "invalid OAuth state") {
		t.Fatalf("error = %q, want invalid state guidance", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "state-secret", "wrong-state-secret", "callback-code")
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("HTTP requests = %d, want none", got)
	}
}

func TestTwitchOAuthLoginFlowRejectsDuplicatePendingState(t *testing.T) {
	now := time.Date(2026, 7, 3, 13, 30, 0, 0, time.UTC)
	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: "https://id.example/oauth2/authorize",
		Now:               func() time.Time { return now },
		StateTTL:          time.Minute,
	})
	request := LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	}

	if _, err := flow.BeginLogin(context.Background(), request); err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}
	_, err := flow.BeginLogin(context.Background(), request)
	if err == nil {
		t.Fatal("second BeginLogin error = nil, want duplicate state rejection")
	}
	if !strings.Contains(err.Error(), "state is already pending") {
		t.Fatalf("second BeginLogin error = %q, want duplicate state guidance", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "state-secret", "client-secret")

	now = now.Add(2 * time.Minute)
	if _, err := flow.BeginLogin(context.Background(), request); err != nil {
		t.Fatalf("BeginLogin after expiry error = %v, want expired pending state replaced", err)
	}
}

func TestTwitchOAuthLoginFlowHandlesDeniedAuthorization(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		t.Fatalf("unexpected HTTP request for denied authorization")
	}))
	defer server.Close()

	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: server.URL + "/authorize",
		TokenEndpoint:     server.URL + "/token",
		ValidateEndpoint:  server.URL + "/validate",
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		State:            NewSecret("state-secret"),
		ExpectedState:    challenge.State,
		Error:            "access_denied",
		ErrorDescription: "user denied access for state-secret",
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want denied authorization")
	}
	if !strings.Contains(err.Error(), "authorization denied") || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("error = %q, want denied authorization guidance", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "state-secret")
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("HTTP requests = %d, want none", got)
	}
}

func TestTwitchOAuthLoginFlowRedactsTwitchTokenErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"code=callback-code client_secret=client-secret state=state-secret refresh_token=refresh-secret"}`)
	}))
	defer server.Close()

	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: server.URL + "/authorize",
		TokenEndpoint:     server.URL,
		ValidateEndpoint:  server.URL + "/validate",
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("state-secret"),
		ExpectedState: challenge.State,
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want token endpoint error")
	}
	if !strings.Contains(err.Error(), "exchange Twitch OAuth code") || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("error = %q, want token exchange guidance", err.Error())
	}
	if !strings.Contains(err.Error(), RedactedSecret) {
		t.Fatalf("error = %q, want redaction marker", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "callback-code", "client-secret", "state-secret", "refresh-secret")
}

func TestTwitchOAuthLoginFlowRedactsValidationErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600,"scope":["chat:read","chat:edit"],"token_type":"bearer"}`)
		case "/validate":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"status":401,"message":"Bearer access-token refresh_token=refresh-token client_secret=client-secret state=state-secret"}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: server.URL + "/authorize",
		TokenEndpoint:     server.URL + "/token",
		ValidateEndpoint:  server.URL + "/validate",
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("state-secret"),
		ExpectedState: challenge.State,
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want validation endpoint error")
	}
	if !strings.Contains(err.Error(), "validate Twitch OAuth token") || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %q, want validation guidance", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "access-token", "refresh-token", "client-secret", "state-secret")
}

func TestTwitchOAuthLoginFlowDoesNotExposeRawWrappedErrors(t *testing.T) {
	rawErr := errors.New("transport leaked code=callback-code client_secret=client-secret state=state-secret")
	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: "https://id.example/oauth2/authorize",
		TokenEndpoint:     "https://id.example/oauth2/token",
		ValidateEndpoint:  "https://id.example/oauth2/validate",
		HTTPClient: &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, rawErr
		})},
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("state-secret"),
		ExpectedState: challenge.State,
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want transport error")
	}
	if errors.Unwrap(err) != nil {
		t.Fatalf("wrapped error = %#v, want no raw non-context cause", errors.Unwrap(err))
	}
	formatted := fmt.Sprintf("%+v %#v", err, err)
	if !strings.Contains(formatted, "exchange Twitch OAuth code") {
		t.Fatalf("formatted error = %q, want exchange action", formatted)
	}
	assertTextDoesNotLeak(t, formatted, "callback-code", "client-secret", "state-secret")
}

func TestTwitchOAuthLoginFlowRedactsBodyReadCancellation(t *testing.T) {
	t.Run("token endpoint error body", func(t *testing.T) {
		flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
			AuthorizeEndpoint: "https://id.example/oauth2/authorize",
			TokenEndpoint:     "https://id.example/oauth2/token",
			ValidateEndpoint:  "https://id.example/oauth2/validate",
			HTTPClient: &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     make(http.Header),
					Body:       errReadCloser{err: context.Canceled},
					Request:    req,
				}, nil
			})},
		})
		challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
			ClientID:     "client-id",
			ClientSecret: NewSecret("client-secret"),
			RedirectURI:  "http://127.0.0.1/callback",
			State:        NewSecret("state-secret"),
		})
		if err != nil {
			t.Fatalf("BeginLogin error = %v", err)
		}

		_, err = flow.CompleteLogin(context.Background(), LoginCallback{
			Code:          NewSecret("callback-code"),
			State:         NewSecret("state-secret"),
			ExpectedState: challenge.State,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CompleteLogin error = %v, want context canceled", err)
		}
		if !strings.Contains(err.Error(), "read Twitch OAuth token response") {
			t.Fatalf("error = %q, want token response read guidance", err.Error())
		}
		assertTextDoesNotLeak(t, fmt.Sprintf("%+v %#v", err, err), "callback-code", "client-secret", "state-secret")
	})

	t.Run("validation endpoint error body", func(t *testing.T) {
		flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
			AuthorizeEndpoint: "https://id.example/oauth2/authorize",
			TokenEndpoint:     "https://id.example/oauth2/token",
			ValidateEndpoint:  "https://id.example/oauth2/validate",
			HTTPClient: &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Path, "/token") {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600,"scope":["chat:read","chat:edit"],"token_type":"bearer"}`)),
						Request:    req,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Header:     make(http.Header),
					Body:       errReadCloser{err: context.Canceled},
					Request:    req,
				}, nil
			})},
		})
		challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
			ClientID:     "client-id",
			ClientSecret: NewSecret("client-secret"),
			RedirectURI:  "http://127.0.0.1/callback",
			State:        NewSecret("state-secret"),
		})
		if err != nil {
			t.Fatalf("BeginLogin error = %v", err)
		}

		_, err = flow.CompleteLogin(context.Background(), LoginCallback{
			Code:          NewSecret("callback-code"),
			State:         NewSecret("state-secret"),
			ExpectedState: challenge.State,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CompleteLogin error = %v, want context canceled", err)
		}
		if !strings.Contains(err.Error(), "read Twitch OAuth validation response") {
			t.Fatalf("error = %q, want validation response read guidance", err.Error())
		}
		assertTextDoesNotLeak(t, fmt.Sprintf("%+v %#v", err, err), "access-token", "refresh-token", "callback-code", "client-secret", "state-secret")
	})
}

func TestTwitchOAuthLoginFlowRejectsMissingScopes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600,"scope":["chat:read"],"token_type":"bearer"}`)
		case "/validate":
			t.Fatal("validation endpoint should not be called when token response lacks required scopes")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: server.URL + "/authorize",
		TokenEndpoint:     server.URL + "/token",
		ValidateEndpoint:  server.URL + "/validate",
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("state-secret"),
		ExpectedState: challenge.State,
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want missing scope error")
	}
	if !strings.Contains(err.Error(), "chat:edit") || !strings.Contains(err.Error(), "approve") {
		t.Fatalf("error = %q, want missing scope guidance", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "access-token", "refresh-token", "callback-code", "client-secret", "state-secret")
}

func TestTwitchOAuthLoginFlowRejectsValidationMissingScopes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600,"scope":["chat:read","chat:edit"],"token_type":"bearer"}`)
		case "/validate":
			fmt.Fprint(w, `{"client_id":"client-id","login":"viewer","scopes":["chat:read"],"user_id":"42","expires_in":3590}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: server.URL + "/authorize",
		TokenEndpoint:     server.URL + "/token",
		ValidateEndpoint:  server.URL + "/validate",
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("state-secret"),
		ExpectedState: challenge.State,
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want validation missing scope error")
	}
	if !strings.Contains(err.Error(), "chat:edit") || !strings.Contains(err.Error(), "approve") {
		t.Fatalf("error = %q, want missing scope guidance", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "access-token", "refresh-token", "callback-code", "client-secret", "state-secret")
}

func TestTwitchOAuthLoginFlowRejectsValidationClientMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600,"scope":["chat:read","chat:edit"],"token_type":"bearer"}`)
		case "/validate":
			fmt.Fprint(w, `{"client_id":"other-client","login":"viewer","scopes":["chat:read","chat:edit"],"user_id":"42","expires_in":3590}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		AuthorizeEndpoint: server.URL + "/authorize",
		TokenEndpoint:     server.URL + "/token",
		ValidateEndpoint:  server.URL + "/validate",
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("state-secret"),
		ExpectedState: challenge.State,
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want validation client mismatch")
	}
	if !strings.Contains(err.Error(), "different Twitch client") {
		t.Fatalf("error = %q, want client mismatch guidance", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "access-token", "refresh-token", "callback-code", "client-secret", "state-secret")
}

func TestTwitchOAuthLoginFlowHonorsTimeoutAndCancellation(t *testing.T) {
	t.Run("request timeout", func(t *testing.T) {
		flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
			AuthorizeEndpoint: "https://id.example/oauth2/authorize",
			TokenEndpoint:     "https://id.example/oauth2/token",
			ValidateEndpoint:  "https://id.example/oauth2/validate",
			HTTPClient: &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			})},
			RequestTimeout: time.Nanosecond,
		})
		challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
			ClientID:     "client-id",
			ClientSecret: NewSecret("client-secret"),
			RedirectURI:  "http://127.0.0.1/callback",
			State:        NewSecret("state-secret"),
		})
		if err != nil {
			t.Fatalf("BeginLogin error = %v", err)
		}

		_, err = flow.CompleteLogin(context.Background(), LoginCallback{
			Code:          NewSecret("callback-code"),
			State:         NewSecret("state-secret"),
			ExpectedState: challenge.State,
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("CompleteLogin error = %v, want context deadline", err)
		}
		if !strings.Contains(err.Error(), "exchange Twitch OAuth code") {
			t.Fatalf("error = %q, want exchange action", err.Error())
		}
		assertTextDoesNotLeak(t, err.Error(), "callback-code", "client-secret", "state-secret")
	})

	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{})
		if _, err := flow.BeginLogin(ctx, LoginRequest{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("BeginLogin error = %v, want context canceled", err)
		}
		if _, err := flow.CompleteLogin(ctx, LoginCallback{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("CompleteLogin error = %v, want context canceled", err)
		}
	})
}

func TestTwitchOAuthLoginFlowRejectsExpiredState(t *testing.T) {
	now := time.Date(2026, 7, 3, 14, 0, 0, 0, time.UTC)
	flow := NewTwitchOAuthLoginFlow(TwitchOAuthLoginFlowConfig{
		Now:      func() time.Time { return now },
		StateTTL: time.Minute,
	})
	challenge, err := flow.BeginLogin(context.Background(), LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}

	now = now.Add(2 * time.Minute)
	_, err = flow.CompleteLogin(context.Background(), LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("state-secret"),
		ExpectedState: challenge.State,
	})
	if err == nil {
		t.Fatal("CompleteLogin error = nil, want expired state")
	}
	if !strings.Contains(err.Error(), "state expired") {
		t.Fatalf("error = %q, want expired state guidance", err.Error())
	}
	assertTextDoesNotLeak(t, err.Error(), "state-secret", "callback-code")
}

func assertFormValue(t *testing.T, values url.Values, key, want string) {
	t.Helper()
	if got := values.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertTextDoesNotLeak(t *testing.T, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.TrimSpace(secret) == "" {
			continue
		}
		if strings.Contains(text, secret) {
			t.Fatalf("text leaked %q: %s", secret, text)
		}
	}
}

type oauthRoundTripFunc func(*http.Request) (*http.Response, error)

func (f oauthRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errReadCloser struct {
	err error
}

func (r errReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r errReadCloser) Close() error {
	return nil
}
