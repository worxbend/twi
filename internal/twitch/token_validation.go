package twitch

import (
	"context"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/auth"
)

const (
	// ScopeChatRead is required by Twitch IRC to read chat.
	ScopeChatRead TokenScope = auth.ScopeChatRead
	// ScopeChatEdit is required by Twitch IRC to send chat messages.
	ScopeChatEdit TokenScope = auth.ScopeChatEdit
)

var requiredIRCScopes = auth.RequiredChatScopes()

// TokenValidator validates a Twitch OAuth token without exposing token
// contents through the result.
type TokenValidator interface {
	ValidateToken(context.Context, TokenCredentials) (TokenValidationResult, error)
}

// TokenCredentials contains the credential metadata a validator needs. The
// token and client secret fields are sensitive and must not be included in
// user-facing errors or diagnostic details.
type TokenCredentials struct {
	Username     string
	OAuthToken   string
	RefreshToken string
	ClientID     string
	ClientSecret string
}

// RefreshAvailable reports whether the current credentials contain enough
// OAuth client information to attempt a refresh flow.
func (c TokenCredentials) RefreshAvailable() bool {
	return strings.TrimSpace(c.RefreshToken) != "" &&
		strings.TrimSpace(c.ClientID) != "" &&
		strings.TrimSpace(c.ClientSecret) != ""
}

// TokenValidationStatus is the credential state reported by a token validator.
type TokenValidationStatus string

const (
	// TokenValidationValid means the token is usable for the requested account
	// and required scope set.
	TokenValidationValid TokenValidationStatus = "valid"

	// TokenValidationMalformed means Twitch or the adapter could not parse or
	// recognize the token as a valid OAuth token.
	TokenValidationMalformed TokenValidationStatus = "malformed"

	// TokenValidationExpired means the access token has expired.
	TokenValidationExpired TokenValidationStatus = "expired"

	// TokenValidationWrongUser means the token belongs to a different Twitch
	// user than the configured username.
	TokenValidationWrongUser TokenValidationStatus = "wrong_user"

	// TokenValidationMissingScope means the token is valid but lacks one or
	// more scopes required for the requested behavior.
	TokenValidationMissingScope TokenValidationStatus = "missing_scope"
)

// TokenScope is a Twitch OAuth scope value.
type TokenScope = auth.Scope

// TokenIdentity is the Twitch user identity associated with a validated token.
type TokenIdentity struct {
	UserID      string
	Login       string
	DisplayName string
}

// TokenValidationResult describes the validated token identity, lifetime,
// scopes, and user-facing status without carrying token contents.
type TokenValidationResult struct {
	Status           TokenValidationStatus
	Identity         TokenIdentity
	Scopes           []TokenScope
	MissingScopes    []TokenScope
	ExpiresAt        time.Time
	RefreshAvailable bool
	Detail           string
}

// Valid reports whether the validator classified the token as valid.
func (r TokenValidationResult) Valid() bool {
	return r.Status == TokenValidationValid
}

// RequiredIRCScopes returns the Twitch OAuth scopes required for IRC read and
// send support.
func RequiredIRCScopes() []TokenScope {
	return append([]TokenScope(nil), requiredIRCScopes...)
}

// MissingRequiredIRCScopes returns the required IRC scopes absent from scopes.
func MissingRequiredIRCScopes(scopes []TokenScope) []TokenScope {
	return MissingScopes(scopes, requiredIRCScopes)
}

// MissingScopes returns required scopes that are not present in got.
func MissingScopes(got, required []TokenScope) []TokenScope {
	return auth.MissingScopes(got, required)
}

// TokenScopes converts non-empty string values into TokenScope values.
func TokenScopes(values ...string) []TokenScope {
	return auth.Scopes(values...)
}
