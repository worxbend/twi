package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLoginRequestDefaultsToRequiredChatScopes(t *testing.T) {
	request := LoginRequest{}
	if got := request.RequiredScopes(); !reflect.DeepEqual(got, RequiredChatScopes()) {
		t.Fatalf("RequiredScopes = %#v, want default chat scopes", got)
	}

	request.Scopes = []Scope{ScopeChatRead}
	got := request.RequiredScopes()
	got[0] = ScopeChatEdit
	if request.Scopes[0] != ScopeChatRead {
		t.Fatalf("RequiredScopes returned mutable request backing slice")
	}
}

func TestLoginResultMissingRequiredScopes(t *testing.T) {
	result := LoginResult{
		Tokens: TokenSet{Scopes: []Scope{ScopeChatRead}},
	}
	if got := result.MissingRequiredScopes(); !reflect.DeepEqual(got, []Scope{ScopeChatEdit}) {
		t.Fatalf("missing = %#v, want chat:edit", got)
	}

	result.Scopes = RequiredChatScopes()
	if got := result.MissingRequiredScopes(); len(got) != 0 {
		t.Fatalf("missing = %#v, want none", got)
	}
}

func TestLoginTypesDoNotFormatSecrets(t *testing.T) {
	values := []any{
		LoginRequest{
			ClientSecret: NewSecret("client-secret"),
			State:        NewSecret("state-secret"),
		},
		LoginChallenge{
			AuthorizationURL: NewSecret("https://id.example/authorize?state=state-secret"),
			State:            NewSecret("state-secret"),
		},
		LoginCallback{
			Code:          NewSecret("callback-code"),
			State:         NewSecret("state-secret"),
			ExpectedState: NewSecret("state-secret"),
		},
		LoginResult{
			Tokens: TokenSet{
				AccessToken:  NewSecret("oauth:access-secret"),
				RefreshToken: NewSecret("refresh-secret"),
			},
		},
	}

	for _, value := range values {
		formatted := fmt.Sprintf("%+v %#v", value, value)
		for _, raw := range []string{
			"client-secret",
			"state-secret",
			"callback-code",
			"oauth:access-secret",
			"access-secret",
			"refresh-secret",
		} {
			if strings.Contains(formatted, raw) {
				t.Fatalf("%T formatted output leaked %q: %s", value, raw, formatted)
			}
		}
	}
}

func TestLoginCallbackDenied(t *testing.T) {
	if !(LoginCallback{Error: "access_denied"}).Denied() {
		t.Fatal("Denied = false, want true")
	}
	if (LoginCallback{Code: NewSecret("code")}).Denied() {
		t.Fatal("Denied = true for callback with code")
	}
}

func TestLoginCallbackFromRequestParsesCallbackValues(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/callback?code=callback-code&state=state-secret&error=access_denied&error_description=user+denied", nil)

	callback := LoginCallbackFromRequest(request, NewSecret("expected-state"))
	if callback.Code.Reveal() != "callback-code" {
		t.Fatalf("code = %q, want callback-code", callback.Code.Reveal())
	}
	if callback.State.Reveal() != "state-secret" {
		t.Fatalf("state = %q, want state-secret", callback.State.Reveal())
	}
	if callback.ExpectedState.Reveal() != "expected-state" {
		t.Fatalf("expected state = %q, want expected-state", callback.ExpectedState.Reveal())
	}
	if callback.Error != "access_denied" || callback.ErrorDescription != "user denied" {
		t.Fatalf("error fields = %q/%q, want provider denial", callback.Error, callback.ErrorDescription)
	}

	formatted := fmt.Sprintf("%+v %#v", callback, callback)
	for _, raw := range []string{"callback-code", "state-secret", "expected-state"} {
		if strings.Contains(formatted, raw) {
			t.Fatalf("formatted callback leaked %q: %s", raw, formatted)
		}
	}
}

func TestFakeLoginFlowQueuesOutcomesAndRecordsRequests(t *testing.T) {
	fake := NewFakeLoginFlow()
	challenge := LoginChallenge{
		AuthorizationURL: NewSecret("https://id.example/authorize?state=state-secret"),
		State:            NewSecret("state-secret"),
		Scopes:           RequiredChatScopes(),
	}
	result := LoginResult{
		Identity: Identity{UserID: "42", Login: "viewer"},
		Tokens: TokenSet{
			AccessToken:  NewSecret("oauth:access-secret"),
			RefreshToken: NewSecret("refresh-secret"),
			Scopes:       RequiredChatScopes(),
		},
	}
	fake.QueueBegin(challenge, nil)
	fake.QueueComplete(result, nil)

	request := LoginRequest{
		ClientID:     "client-id",
		ClientSecret: NewSecret("client-secret"),
		RedirectURI:  "http://127.0.0.1/callback",
		State:        NewSecret("state-secret"),
	}
	gotChallenge, err := fake.BeginLogin(context.Background(), request)
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}
	if gotChallenge.AuthorizationURL.Reveal() != challenge.AuthorizationURL.Reveal() {
		t.Fatalf("challenge URL = %q, want queued URL", gotChallenge.AuthorizationURL.Reveal())
	}

	callback := LoginCallback{
		Code:          NewSecret("callback-code"),
		State:         NewSecret("state-secret"),
		ExpectedState: NewSecret("state-secret"),
	}
	gotResult, err := fake.CompleteLogin(context.Background(), callback)
	if err != nil {
		t.Fatalf("CompleteLogin error = %v", err)
	}
	if gotResult.Identity.Login != "viewer" || gotResult.Tokens.AccessToken.Reveal() != "oauth:access-secret" {
		t.Fatalf("result = %#v, want queued login result", gotResult)
	}

	beginRequests := fake.BeginRequests()
	if len(beginRequests) != 1 || beginRequests[0].ClientSecret.Reveal() != "client-secret" {
		t.Fatalf("begin requests = %#v, want original request", beginRequests)
	}
	completeCalls := fake.CompleteCalls()
	if len(completeCalls) != 1 || completeCalls[0].Code.Reveal() != "callback-code" {
		t.Fatalf("complete calls = %#v, want original callback", completeCalls)
	}
}

func TestFakeLoginFlowDefaultsAndCancellation(t *testing.T) {
	wantErr := errors.New("login failed")
	fake := NewFakeLoginFlow()
	fake.SetDefaultBegin(LoginChallenge{}, wantErr)
	fake.SetDefaultComplete(LoginResult{}, wantErr)

	if _, err := fake.BeginLogin(context.Background(), LoginRequest{}); !errors.Is(err, wantErr) {
		t.Fatalf("BeginLogin default error = %v, want %v", err, wantErr)
	}
	if _, err := fake.CompleteLogin(context.Background(), LoginCallback{}); !errors.Is(err, wantErr) {
		t.Fatalf("CompleteLogin default error = %v, want %v", err, wantErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fake.BeginLogin(ctx, LoginRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("BeginLogin canceled error = %v, want context.Canceled", err)
	}
	if _, err := fake.CompleteLogin(ctx, LoginCallback{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompleteLogin canceled error = %v, want context.Canceled", err)
	}
	if got := len(fake.BeginRequests()); got != 1 {
		t.Fatalf("begin requests = %d, want only uncanceled default call", got)
	}
	if got := len(fake.CompleteCalls()); got != 1 {
		t.Fatalf("complete calls = %d, want only uncanceled default call", got)
	}
}

func TestFakeLoginFlowClonesScopeSlices(t *testing.T) {
	fake := NewFakeLoginFlow()
	requestScopes := []Scope{ScopeChatRead}
	challengeScopes := []Scope{ScopeChatRead}
	resultScopes := []Scope{ScopeChatRead}
	tokenScopes := []Scope{ScopeChatRead}

	fake.QueueBegin(LoginChallenge{Scopes: challengeScopes}, nil)
	fake.QueueComplete(LoginResult{
		Scopes: resultScopes,
		Tokens: TokenSet{Scopes: tokenScopes},
	}, nil)

	challengeScopes[0] = ScopeChatEdit
	resultScopes[0] = ScopeChatEdit
	tokenScopes[0] = ScopeChatEdit

	challenge, err := fake.BeginLogin(context.Background(), LoginRequest{Scopes: requestScopes})
	if err != nil {
		t.Fatalf("BeginLogin error = %v", err)
	}
	requestScopes[0] = ScopeChatEdit
	if got := challenge.Scopes; !reflect.DeepEqual(got, []Scope{ScopeChatRead}) {
		t.Fatalf("challenge scopes = %#v, want queued snapshot", got)
	}
	challenge.Scopes[0] = ScopeChatEdit

	result, err := fake.CompleteLogin(context.Background(), LoginCallback{})
	if err != nil {
		t.Fatalf("CompleteLogin error = %v", err)
	}
	if got := result.Scopes; !reflect.DeepEqual(got, []Scope{ScopeChatRead}) {
		t.Fatalf("result scopes = %#v, want queued snapshot", got)
	}
	if got := result.Tokens.Scopes; !reflect.DeepEqual(got, []Scope{ScopeChatRead}) {
		t.Fatalf("token scopes = %#v, want queued snapshot", got)
	}

	requests := fake.BeginRequests()
	if got := requests[0].Scopes; !reflect.DeepEqual(got, []Scope{ScopeChatRead}) {
		t.Fatalf("recorded request scopes = %#v, want request snapshot", got)
	}
	requests[0].Scopes[0] = ScopeChatEdit
	if got := fake.BeginRequests()[0].Scopes; !reflect.DeepEqual(got, []Scope{ScopeChatRead}) {
		t.Fatalf("recorded request scopes after caller mutation = %#v, want stable snapshot", got)
	}
}
