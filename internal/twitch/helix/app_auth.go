package helix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/jsonbody"
)

// AppTokenClient obtains only client-credentials tokens; it never falls back to a user token.
type AppTokenClient struct {
	client                *http.Client
	clientID              string
	secret                auth.Secret
	tokenURL, validateURL string
}
type AppTokenConfig struct {
	HTTPClient            *http.Client
	ClientID              string
	ClientSecret          auth.Secret
	TokenURL, ValidateURL string
}

func NewAppTokenClient(config AppTokenConfig) *AppTokenClient {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	client := *config.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &AppTokenClient{client: &client, clientID: config.ClientID, secret: config.ClientSecret, tokenURL: endpointOrDefault(config.TokenURL, "https://id.twitch.tv/oauth2/token"), validateURL: endpointOrDefault(config.ValidateURL, "https://id.twitch.tv/oauth2/validate")}
}
func (c *AppTokenClient) Acquire(ctx context.Context) (auth.Secret, time.Time, error) {
	if c.clientID == "" || !c.secret.Present() {
		return auth.NewSecret(""), time.Time{}, fmt.Errorf("app sending requires client ID and client secret: %w", twitch.ErrAuthFailed)
	}
	values := url.Values{"client_id": {c.clientID}, "client_secret": {c.secret.Reveal()}, "grant_type": {"client_credentials"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return auth.NewSecret(""), time.Time{}, errors.New("cannot create app token request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.client.Do(request)
	if err != nil {
		return auth.NewSecret(""), time.Time{}, errors.New("app token endpoint unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode == 400 || response.StatusCode == 401 || response.StatusCode == 403 {
			return auth.NewSecret(""), time.Time{}, fmt.Errorf("app token rejected (HTTP %d): %w", response.StatusCode, twitch.ErrAuthFailed)
		}
		return auth.NewSecret(""), time.Time{}, fmt.Errorf("app token endpoint unavailable (HTTP %d)", response.StatusCode)
	}
	var result struct {
		Token   string `json:"access_token"`
		Expires int64  `json:"expires_in"`
		Type    string `json:"token_type"`
	}
	if jsonbody.Decode(response.Body, 64*1024, &result) != nil || result.Token == "" || result.Expires <= 0 || result.Expires > 365*24*60*60 || !strings.EqualFold(result.Type, "bearer") {
		return auth.NewSecret(""), time.Time{}, errors.New("invalid app token response")
	}
	token := auth.NewSecret(result.Token)
	if err := c.Validate(ctx, token); err != nil {
		return auth.NewSecret(""), time.Time{}, err
	}
	return token, time.Now().Add(time.Duration(result.Expires) * time.Second), nil
}
func (c *AppTokenClient) Validate(ctx context.Context, token auth.Secret) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.validateURL, nil)
	if err != nil {
		return errors.New("cannot create app validation request")
	}
	request.Header.Set("Authorization", "OAuth "+token.Reveal())
	response, err := c.client.Do(request)
	if err != nil {
		return errors.New("app token validation unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return fmt.Errorf("app token validation rejected (HTTP %d): %w", response.StatusCode, twitch.ErrAuthFailed)
		}
		return fmt.Errorf("app validation endpoint unavailable (HTTP %d)", response.StatusCode)
	}
	var result struct {
		ClientID string `json:"client_id"`
		UserID   string `json:"user_id"`
		Login    string `json:"login"`
		Expires  int64  `json:"expires_in"`
	}
	if jsonbody.Decode(response.Body, 64*1024, &result) != nil {
		return errors.New("invalid app validation response")
	}
	if result.ClientID != c.clientID || result.UserID != "" || result.Login != "" || result.Expires <= 0 {
		return fmt.Errorf("token is not an app token for this client: %w", twitch.ErrAuthFailed)
	}
	return nil
}

type AuthorizationGrant struct {
	UserID     string       `json:"user_id"`
	Login      string       `json:"user_login"`
	Scopes     []auth.Scope `json:"scopes"`
	Authorized bool         `json:"has_authorized"`
}
type AuthorizationClient struct {
	transport
	endpoint string
}

func NewAuthorizationClient(config ClientConfig) *AuthorizationClient {
	return &AuthorizationClient{transport: newTransport(config.HTTPClient, config.ClientID, config.OAuthTokenSource, config.OAuthToken, config.RetryPolicy), endpoint: endpointOrDefault(config.Endpoint, "https://api.twitch.tv/helix/authorization/users")}
}
func (c *AuthorizationClient) GetGrants(ctx context.Context, ids []string) ([]AuthorizationGrant, error) {
	if len(ids) < 1 || len(ids) > 10 {
		return nil, errors.New("authorization lookup requires 1 to 10 user IDs")
	}
	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, errors.New("invalid authorization endpoint")
	}
	query := parsed.Query()
	wanted := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			return nil, errors.New("authorization lookup requires user IDs")
		}
		query.Add("user_id", id)
		wanted[id] = true
	}
	parsed.RawQuery = query.Encode()
	result, err := getJSON[struct {
		Data []AuthorizationGrant `json:"data"`
	}](ctx, c.transport, parsed.String(), errorLabels{action: "get Twitch application grants", readAction: "read application grants", subject: "application grants", endpoint: "Get Authorization By User"})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, grant := range result.Data {
		if !wanted[grant.UserID] || seen[grant.UserID] {
			return nil, errors.New("invalid application grant identities")
		}
		seen[grant.UserID] = true
	}
	if len(seen) != len(wanted) {
		return nil, errors.New("application grant response incomplete")
	}
	return result.Data, nil
}
