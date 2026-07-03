package auth

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// LoginFlow is the boundary for interactive OAuth login. Implementations may
// talk to Twitch or local callback HTTP servers, but callers should only need
// these typed requests and responses.
type LoginFlow interface {
	BeginLogin(context.Context, LoginRequest) (LoginChallenge, error)
	CompleteLogin(context.Context, LoginCallback) (LoginResult, error)
}

// LoginRequest describes the start of an OAuth login attempt. Sensitive fields
// use Secret so accidental formatting does not print raw values.
type LoginRequest struct {
	ClientID      string
	ClientSecret  Secret
	RedirectURI   string
	Scopes        []Scope
	State         Secret
	CodeChallenge string
	LoginHint     string
}

// RequiredScopes returns the request scopes, or the default chat read/send
// scopes when no explicit scopes were provided.
func (r LoginRequest) RequiredScopes() []Scope {
	if len(r.Scopes) == 0 {
		return RequiredChatScopes()
	}
	return append([]Scope(nil), r.Scopes...)
}

// Redactor returns a redactor configured with request secrets.
func (r LoginRequest) Redactor() Redactor {
	return NewRedactor(r.ClientSecret, r.State)
}

// LoginChallenge is the user-visible authorization challenge produced by a
// login flow. AuthorizationURL is sensitive because it can contain state or
// challenge material, so callers must reveal it deliberately.
type LoginChallenge struct {
	AuthorizationURL Secret
	State            Secret
	Scopes           []Scope
	ExpiresAt        time.Time
}

// Redactor returns a redactor configured with challenge secrets.
func (c LoginChallenge) Redactor() Redactor {
	return NewRedactor(c.AuthorizationURL, c.State)
}

// LoginCallback contains the OAuth callback values received after a user
// authorizes or denies the login request.
type LoginCallback struct {
	Code             Secret
	State            Secret
	ExpectedState    Secret
	Error            string
	ErrorDescription string
}

// LoginCallbackFromRequest extracts OAuth callback values from an HTTP request.
// The returned code and state values are Secret values so accidental formatting
// remains redacted.
func LoginCallbackFromRequest(r *http.Request, expectedState Secret) LoginCallback {
	callback := LoginCallback{ExpectedState: expectedState}
	if r == nil || r.URL == nil {
		return callback
	}

	query := r.URL.Query()
	callback.Code = NewSecret(query.Get("code"))
	callback.State = NewSecret(query.Get("state"))
	callback.Error = strings.TrimSpace(query.Get("error"))
	callback.ErrorDescription = strings.TrimSpace(query.Get("error_description"))
	return callback
}

// Denied reports whether the callback contains a provider denial instead of an
// authorization code.
func (c LoginCallback) Denied() bool {
	return strings.TrimSpace(c.Error) != ""
}

// Redactor returns a redactor configured with callback secrets.
func (c LoginCallback) Redactor() Redactor {
	return NewRedactor(c.Code, c.State, c.ExpectedState)
}

// Identity is the Twitch user identity associated with a completed login.
type Identity struct {
	UserID      string
	Login       string
	DisplayName string
}

// TokenSet is the OAuth credential material returned by a completed login.
// AccessToken and RefreshToken are Secret values and do not print raw tokens
// through fmt by default.
type TokenSet struct {
	AccessToken  Secret
	RefreshToken Secret
	TokenType    string
	ExpiresAt    time.Time
	Scopes       []Scope
}

// RefreshAvailable reports whether the token response included a refresh
// token.
func (t TokenSet) RefreshAvailable() bool {
	return t.RefreshToken.Present()
}

// Redactor returns a redactor configured with token secrets.
func (t TokenSet) Redactor() Redactor {
	return NewRedactor(t.AccessToken, t.RefreshToken)
}

// LoginResult is the typed result of a completed OAuth login. It carries token
// values for the caller but does not decide where or whether they are stored.
type LoginResult struct {
	Identity Identity
	Tokens   TokenSet
	Scopes   []Scope
}

// MissingRequiredScopes returns the default chat read/send scopes absent from
// the login result.
func (r LoginResult) MissingRequiredScopes() []Scope {
	scopes := r.Scopes
	if len(scopes) == 0 {
		scopes = r.Tokens.Scopes
	}
	return MissingScopes(scopes, RequiredChatScopes())
}

// Redactor returns a redactor configured with all result token secrets.
func (r LoginResult) Redactor() Redactor {
	return r.Tokens.Redactor()
}
