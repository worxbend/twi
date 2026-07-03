package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTwitchOAuthAuthorizeURL is Twitch's production OAuth authorization
	// endpoint for authorization-code login.
	DefaultTwitchOAuthAuthorizeURL = "https://id.twitch.tv/oauth2/authorize"
	// DefaultTwitchOAuthTokenURL is Twitch's production OAuth token endpoint.
	DefaultTwitchOAuthTokenURL = "https://id.twitch.tv/oauth2/token"
	// DefaultTwitchOAuthValidateURL is Twitch's production OAuth validation
	// endpoint.
	DefaultTwitchOAuthValidateURL = "https://id.twitch.tv/oauth2/validate"
)

const (
	defaultOAuthStateTTL       = 10 * time.Minute
	defaultOAuthRequestTimeout = 15 * time.Second
	maxOAuthResponseBodySize   = 4096
)

// TwitchOAuthLoginFlowConfig configures the Twitch authorization-code login
// flow. Zero endpoint values use Twitch production OAuth endpoints. A zero
// RequestTimeout uses a conservative default so token HTTP calls stay bounded.
type TwitchOAuthLoginFlowConfig struct {
	AuthorizeEndpoint string
	TokenEndpoint     string
	ValidateEndpoint  string
	HTTPClient        *http.Client
	Now               func() time.Time
	StateGenerator    func() (Secret, error)
	StateTTL          time.Duration
	RequestTimeout    time.Duration
}

// TwitchOAuthLoginFlow implements LoginFlow using Twitch's OAuth
// authorization-code flow and token validation endpoint.
type TwitchOAuthLoginFlow struct {
	authorizeEndpoint string
	tokenEndpoint     string
	validateEndpoint  string
	httpClient        *http.Client
	now               func() time.Time
	stateGenerator    func() (Secret, error)
	stateTTL          time.Duration
	requestTimeout    time.Duration

	mu      sync.Mutex
	pending map[string]oauthLoginAttempt
}

var _ LoginFlow = (*TwitchOAuthLoginFlow)(nil)

// NewTwitchOAuthLoginFlow creates a Twitch OAuth LoginFlow. The returned flow
// performs no network I/O until CompleteLogin is called.
func NewTwitchOAuthLoginFlow(cfg TwitchOAuthLoginFlowConfig) *TwitchOAuthLoginFlow {
	authorizeEndpoint := strings.TrimSpace(cfg.AuthorizeEndpoint)
	if authorizeEndpoint == "" {
		authorizeEndpoint = DefaultTwitchOAuthAuthorizeURL
	}
	tokenEndpoint := strings.TrimSpace(cfg.TokenEndpoint)
	if tokenEndpoint == "" {
		tokenEndpoint = DefaultTwitchOAuthTokenURL
	}
	validateEndpoint := strings.TrimSpace(cfg.ValidateEndpoint)
	if validateEndpoint == "" {
		validateEndpoint = DefaultTwitchOAuthValidateURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	stateGenerator := cfg.StateGenerator
	if stateGenerator == nil {
		stateGenerator = randomOAuthState
	}
	stateTTL := cfg.StateTTL
	if stateTTL <= 0 {
		stateTTL = defaultOAuthStateTTL
	}
	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultOAuthRequestTimeout
	}

	return &TwitchOAuthLoginFlow{
		authorizeEndpoint: authorizeEndpoint,
		tokenEndpoint:     tokenEndpoint,
		validateEndpoint:  validateEndpoint,
		httpClient:        httpClient,
		now:               now,
		stateGenerator:    stateGenerator,
		stateTTL:          stateTTL,
		requestTimeout:    requestTimeout,
		pending:           make(map[string]oauthLoginAttempt),
	}
}

// BeginLogin creates a Twitch authorization URL and records the OAuth state
// needed to complete the callback later.
func (f *TwitchOAuthLoginFlow) BeginLogin(ctx context.Context, request LoginRequest) (LoginChallenge, error) {
	if err := ctx.Err(); err != nil {
		return LoginChallenge{}, oauthSafeError("start Twitch OAuth login", err, request.Redactor())
	}

	attempt, err := f.newAttempt(request)
	if err != nil {
		return LoginChallenge{}, err
	}
	authorizationURL, err := f.authorizationURL(attempt)
	if err != nil {
		return LoginChallenge{}, oauthSafeError("build Twitch OAuth authorization URL", err, request.Redactor())
	}

	if err := f.storeAttempt(attempt); err != nil {
		return LoginChallenge{}, err
	}

	return LoginChallenge{
		AuthorizationURL: NewSecret(authorizationURL),
		State:            attempt.State,
		Scopes:           cloneScopes(attempt.Scopes),
		ExpiresAt:        attempt.ExpiresAt,
	}, nil
}

// CompleteLogin validates the callback state, exchanges the authorization code
// for tokens, validates the returned access token, and returns a typed result.
func (f *TwitchOAuthLoginFlow) CompleteLogin(ctx context.Context, callback LoginCallback) (LoginResult, error) {
	if err := ctx.Err(); err != nil {
		return LoginResult{}, oauthSafeError("complete Twitch OAuth login", err, callback.Redactor())
	}

	state, err := validateOAuthCallbackState(callback)
	if err != nil {
		return LoginResult{}, err
	}

	attempt, err := f.consumeAttempt(state)
	if err != nil {
		return LoginResult{}, err
	}
	redactor := NewRedactor(attempt.ClientSecret, attempt.State, callback.Code, callback.State, callback.ExpectedState)

	if callback.Denied() {
		return LoginResult{}, oauthDeniedError(callback, redactor)
	}

	code := strings.TrimSpace(callback.Code.Reveal())
	if code == "" {
		return LoginResult{}, errors.New("complete Twitch OAuth login: callback did not include an authorization code; restart login")
	}

	httpCtx, cancel := f.httpContext(ctx)
	defer cancel()

	tokens, err := f.exchangeCode(httpCtx, attempt, callback.Code, redactor)
	if err != nil {
		return LoginResult{}, err
	}

	tokenRedactor := NewRedactor(attempt.ClientSecret, attempt.State, callback.Code, tokens.AccessToken, tokens.RefreshToken)
	validation, err := f.validateAccessToken(httpCtx, tokens.AccessToken, tokenRedactor)
	if err != nil {
		return LoginResult{}, err
	}
	if validation.ClientID != attempt.ClientID {
		return LoginResult{}, errors.New("validate Twitch OAuth token: validated token belongs to a different Twitch client; restart login with the configured client ID")
	}

	scopes := validation.Scopes
	if len(scopes) == 0 {
		scopes = tokens.Scopes
	}
	if missing := MissingScopes(scopes, attempt.Scopes); len(missing) > 0 {
		return LoginResult{}, missingScopesError("Twitch OAuth token", missing, attempt.Scopes)
	}

	expiresAt := tokens.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = validation.ExpiresAt
	}

	tokenSet := TokenSet{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    tokens.TokenType,
		ExpiresAt:    expiresAt,
		Scopes:       cloneScopes(scopes),
	}
	return LoginResult{
		Identity: validation.Identity,
		Tokens:   tokenSet,
		Scopes:   cloneScopes(scopes),
	}, nil
}

type oauthLoginAttempt struct {
	ClientID     string
	ClientSecret Secret
	RedirectURI  string
	Scopes       []Scope
	State        Secret
	ExpiresAt    time.Time
}

type oauthExchangedToken struct {
	AccessToken  Secret
	RefreshToken Secret
	TokenType    string
	ExpiresAt    time.Time
	Scopes       []Scope
}

type oauthValidatedToken struct {
	ClientID  string
	Identity  Identity
	Scopes    []Scope
	ExpiresAt time.Time
}

type oauthTokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int64    `json:"expires_in"`
	Scope        []string `json:"scope"`
	TokenType    string   `json:"token_type"`
}

type oauthValidateResponse struct {
	ClientID  string   `json:"client_id"`
	Login     string   `json:"login"`
	Scopes    []string `json:"scopes"`
	UserID    string   `json:"user_id"`
	ExpiresIn int64    `json:"expires_in"`
}

type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Message          string `json:"message"`
}

type redactedWrapError struct {
	message string
	cause   error
}

func (e redactedWrapError) Error() string {
	return e.message
}

func (e redactedWrapError) GoString() string {
	return e.message
}

func (e redactedWrapError) Unwrap() error {
	return e.cause
}

func (f *TwitchOAuthLoginFlow) newAttempt(request LoginRequest) (oauthLoginAttempt, error) {
	clientID := strings.TrimSpace(request.ClientID)
	if clientID == "" {
		return oauthLoginAttempt{}, errors.New("start Twitch OAuth login: missing client ID")
	}
	if !request.ClientSecret.Present() {
		return oauthLoginAttempt{}, errors.New("start Twitch OAuth login: missing client secret")
	}
	redirectURI := strings.TrimSpace(request.RedirectURI)
	if redirectURI == "" {
		return oauthLoginAttempt{}, errors.New("start Twitch OAuth login: missing redirect URI")
	}
	scopes := request.RequiredScopes()
	if len(scopes) == 0 {
		return oauthLoginAttempt{}, errors.New("start Twitch OAuth login: at least one OAuth scope is required")
	}

	state := request.State
	if !state.Present() {
		generated, err := f.stateGenerator()
		if err != nil {
			return oauthLoginAttempt{}, oauthSafeError("generate Twitch OAuth state", err, request.Redactor())
		}
		state = generated
	}
	if !state.Present() {
		return oauthLoginAttempt{}, errors.New("start Twitch OAuth login: OAuth state generator returned an empty state")
	}

	return oauthLoginAttempt{
		ClientID:     clientID,
		ClientSecret: request.ClientSecret,
		RedirectURI:  redirectURI,
		Scopes:       cloneScopes(scopes),
		State:        state,
		ExpiresAt:    f.now().Add(f.stateTTL),
	}, nil
}

func (f *TwitchOAuthLoginFlow) authorizationURL(attempt oauthLoginAttempt) (string, error) {
	authorizationURL, err := url.Parse(f.authorizeEndpoint)
	if err != nil {
		return "", err
	}
	if authorizationURL.Scheme == "" || authorizationURL.Host == "" {
		return "", errors.New("authorization endpoint must be an absolute URL")
	}

	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", attempt.ClientID)
	query.Set("redirect_uri", attempt.RedirectURI)
	query.Set("scope", strings.Join(ScopeValues(attempt.Scopes), " "))
	query.Set("state", attempt.State.Reveal())
	authorizationURL.RawQuery = query.Encode()
	return authorizationURL.String(), nil
}

func (f *TwitchOAuthLoginFlow) storeAttempt(attempt oauthLoginAttempt) error {
	state := attempt.State.Reveal()
	now := f.now()

	f.mu.Lock()
	defer f.mu.Unlock()

	if existing, ok := f.pending[state]; ok {
		if now.Before(existing.ExpiresAt) || now.Equal(existing.ExpiresAt) {
			return errors.New("start Twitch OAuth login: OAuth state is already pending; restart login")
		}
		delete(f.pending, state)
	}
	f.pending[state] = attempt
	return nil
}

func validateOAuthCallbackState(callback LoginCallback) (string, error) {
	state := strings.TrimSpace(callback.State.Reveal())
	if state == "" {
		return "", errors.New("complete Twitch OAuth login: callback did not include OAuth state; restart login")
	}

	expected := strings.TrimSpace(callback.ExpectedState.Reveal())
	if expected != "" && subtle.ConstantTimeCompare([]byte(state), []byte(expected)) != 1 {
		return "", errors.New("complete Twitch OAuth login: invalid OAuth state; restart login")
	}
	return state, nil
}

func (f *TwitchOAuthLoginFlow) consumeAttempt(state string) (oauthLoginAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	attempt, ok := f.pending[state]
	if !ok {
		return oauthLoginAttempt{}, errors.New("complete Twitch OAuth login: OAuth state is unknown or expired; restart login")
	}
	if f.now().After(attempt.ExpiresAt) {
		delete(f.pending, state)
		return oauthLoginAttempt{}, errors.New("complete Twitch OAuth login: OAuth state expired; restart login")
	}
	delete(f.pending, state)
	return attempt, nil
}

func (f *TwitchOAuthLoginFlow) httpContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if f.requestTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, f.requestTimeout)
}

func (f *TwitchOAuthLoginFlow) exchangeCode(ctx context.Context, attempt oauthLoginAttempt, code Secret, redactor Redactor) (oauthExchangedToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", attempt.ClientID)
	form.Set("client_secret", attempt.ClientSecret.Reveal())
	form.Set("code", code.Reveal())
	form.Set("redirect_uri", attempt.RedirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthExchangedToken{}, oauthSafeError("create Twitch OAuth token request", err, redactor)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return oauthExchangedToken{}, oauthSafeError("exchange Twitch OAuth code", err, redactor)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, err := oauthResponseDetail(resp.Body)
		if err != nil {
			return oauthExchangedToken{}, oauthSafeError("read Twitch OAuth token response", err, redactor)
		}
		return oauthExchangedToken{}, oauthStatusError("exchange Twitch OAuth code", resp.StatusCode, detail, redactor)
	}

	var decoded oauthTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOAuthResponseBodySize)).Decode(&decoded); err != nil {
		return oauthExchangedToken{}, oauthSafeError("decode Twitch OAuth token response", err, redactor)
	}

	accessToken := strings.TrimSpace(decoded.AccessToken)
	if accessToken == "" {
		return oauthExchangedToken{}, errors.New("exchange Twitch OAuth code: token response did not include an access token")
	}

	scopes := Scopes(decoded.Scope...)
	if missing := MissingScopes(scopes, attempt.Scopes); len(missing) > 0 {
		return oauthExchangedToken{}, missingScopesError("Twitch OAuth token response", missing, attempt.Scopes)
	}

	tokens := oauthExchangedToken{
		AccessToken:  NewSecret(accessToken),
		RefreshToken: NewSecret(strings.TrimSpace(decoded.RefreshToken)),
		TokenType:    strings.TrimSpace(decoded.TokenType),
		Scopes:       cloneScopes(scopes),
	}
	if decoded.ExpiresIn > 0 {
		tokens.ExpiresAt = f.now().Add(time.Duration(decoded.ExpiresIn) * time.Second)
	}
	return tokens, nil
}

func (f *TwitchOAuthLoginFlow) validateAccessToken(ctx context.Context, accessToken Secret, redactor Redactor) (oauthValidatedToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.validateEndpoint, nil)
	if err != nil {
		return oauthValidatedToken{}, oauthSafeError("create Twitch OAuth validation request", err, redactor)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "OAuth "+accessToken.Reveal())

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return oauthValidatedToken{}, oauthSafeError("validate Twitch OAuth token", err, redactor)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, err := oauthResponseDetail(resp.Body)
		if err != nil {
			return oauthValidatedToken{}, oauthSafeError("read Twitch OAuth validation response", err, redactor)
		}
		return oauthValidatedToken{}, oauthStatusError("validate Twitch OAuth token", resp.StatusCode, detail, redactor)
	}

	var decoded oauthValidateResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOAuthResponseBodySize)).Decode(&decoded); err != nil {
		return oauthValidatedToken{}, oauthSafeError("decode Twitch OAuth validation response", err, redactor)
	}
	if strings.TrimSpace(decoded.UserID) == "" || strings.TrimSpace(decoded.Login) == "" {
		return oauthValidatedToken{}, errors.New("validate Twitch OAuth token: validation response did not include a user identity")
	}
	if strings.TrimSpace(decoded.ClientID) == "" {
		return oauthValidatedToken{}, errors.New("validate Twitch OAuth token: validation response did not include a client ID")
	}
	if decoded.ExpiresIn <= 0 {
		return oauthValidatedToken{}, errors.New("validate Twitch OAuth token: Twitch reported an expired access token; restart login")
	}

	return oauthValidatedToken{
		ClientID: strings.TrimSpace(decoded.ClientID),
		Identity: Identity{
			UserID: strings.TrimSpace(decoded.UserID),
			Login:  strings.TrimSpace(decoded.Login),
		},
		Scopes:    Scopes(decoded.Scopes...),
		ExpiresAt: f.now().Add(time.Duration(decoded.ExpiresIn) * time.Second),
	}, nil
}

func oauthDeniedError(callback LoginCallback, redactor Redactor) error {
	code := strings.TrimSpace(callback.Error)
	if code == "" {
		code = "access_denied"
	}
	detail := strings.TrimSpace(callback.ErrorDescription)
	if detail != "" {
		return errors.New(redactor.Redact("complete Twitch OAuth login: Twitch authorization denied (" + code + "): " + detail))
	}
	return errors.New(redactor.Redact("complete Twitch OAuth login: Twitch authorization denied (" + code + ")"))
}

func missingScopesError(source string, missing, required []Scope) error {
	return errors.New(source + " is missing required OAuth scope(s): " + strings.Join(ScopeValues(missing), ", ") + "; restart login and approve: " + strings.Join(ScopeValues(required), ", "))
}

func oauthStatusError(action string, statusCode int, detail string, redactor Redactor) error {
	message := action + ": Twitch returned HTTP " + strconv.Itoa(statusCode)
	if strings.TrimSpace(detail) != "" {
		message += ": " + strings.TrimSpace(detail)
	}
	return errors.New(redactor.Redact(message))
}

func oauthSafeError(action string, err error, redactor Redactor) error {
	if err == nil {
		return nil
	}
	var cause error
	switch {
	case errors.Is(err, context.Canceled):
		cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		cause = context.DeadlineExceeded
	}
	return redactedWrapError{
		message: redactor.Redact(action + ": " + err.Error()),
		cause:   cause,
	}
}

func oauthResponseDetail(body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxOAuthResponseBodySize))
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", nil
	}

	var decoded oauthErrorResponse
	if err := json.Unmarshal(data, &decoded); err == nil {
		for _, value := range []string{decoded.ErrorDescription, decoded.Message, decoded.Error} {
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value), nil
			}
		}
	}
	return text, nil
}

func randomOAuthState() (Secret, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return NewSecret(base64.RawURLEncoding.EncodeToString(data[:])), nil
}
