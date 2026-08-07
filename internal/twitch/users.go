package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

const defaultHelixUsersURL = "https://api.twitch.tv/helix/users"

// UserLookup resolves Twitch user profile metadata through a Twitch API
// boundary. Implementations must batch IDs and logins instead of issuing one
// request per chat message.
type UserLookup interface {
	GetUsers(context.Context, UserLookupRequest) ([]UserIdentity, error)
}

// UserLookupRequest identifies Twitch users by ID and/or login. Twitch Helix
// accepts up to 100 combined identifiers per Get Users request.
type UserLookupRequest struct {
	UserIDs    []string
	UserLogins []string
}

// UserIdentity contains the user profile fields needed by avatar resolution.
type UserIdentity struct {
	UserID          string
	Login           string
	DisplayName     string
	ProfileImageURL string
}

// HelixUsersClientConfig configures the Twitch Helix Get Users adapter.
// Endpoint, HTTPClient, and Now are injectable for deterministic fake HTTP
// tests; zero values use Twitch's production endpoint and default HTTP client.
type HelixUsersClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource func() string
	Endpoint         string
	HTTPClient       *http.Client
	ClientID         string
	OAuthToken       string
}

// HelixUsersClient resolves Twitch users through Helix Get Users.
type HelixUsersClient struct {
	endpoint         string
	httpClient       *http.Client
	clientID         string
	oauthTokenSource func() string
}

var _ UserLookup = (*HelixUsersClient)(nil)

// NewHelixUsersClient creates a UserLookup backed by Twitch Helix HTTP. The
// returned client performs no network I/O until GetUsers is called.
func NewHelixUsersClient(cfg HelixUsersClientConfig) *HelixUsersClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultHelixUsersURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixUsersClient{
		endpoint:         endpoint,
		httpClient:       httpClient,
		clientID:         strings.TrimSpace(cfg.ClientID),
		oauthTokenSource: resolveTokenSource(cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetUsers performs one Helix Get Users request for the supplied unique IDs
// and logins. Empty requests return without network I/O.
func (c *HelixUsersClient) GetUsers(ctx context.Context, req UserLookupRequest) ([]UserIdentity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := uniqueNonEmpty(req.UserIDs)
	logins := uniqueLowerNonEmpty(req.UserLogins)
	if len(ids)+len(logins) == 0 {
		return nil, nil
	}

	endpoint, err := c.usersURL(ids, logins)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch user lookup request", err, c.token())
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch user lookup request", err, c.token())
	}
	if c.clientID != "" {
		httpReq.Header.Set("Client-Id", c.clientID)
	}
	token := accessTokenForValidation(c.token())
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, credentialSafeUserError("lookup Twitch users", err, c.token())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, err := readSmallBody(resp.Body)
		if err != nil {
			return nil, credentialSafeUserError("read Twitch user lookup response", err, c.token())
		}
		if detail != "" {
			detail = ": " + detail
		}
		return nil, credentialSafeUserError(
			"lookup Twitch users",
			fmt.Errorf("twitch Get Users returned HTTP %d%s", resp.StatusCode, detail),
			c.token(),
		)
	}

	var decoded helixUsersResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, credentialSafeUserError("decode Twitch user lookup response", err, c.token())
	}
	users := make([]UserIdentity, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		if strings.TrimSpace(item.ID) == "" && strings.TrimSpace(item.Login) == "" {
			continue
		}
		users = append(users, UserIdentity{
			UserID:          strings.TrimSpace(item.ID),
			Login:           strings.TrimSpace(item.Login),
			DisplayName:     strings.TrimSpace(item.DisplayName),
			ProfileImageURL: strings.TrimSpace(item.ProfileImageURL),
		})
	}
	return users, nil
}

func (c *HelixUsersClient) usersURL(ids, logins []string) (string, error) {
	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for _, id := range ids {
		query.Add("id", id)
	}
	for _, login := range logins {
		query.Add("login", login)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type helixUsersResponse struct {
	Data []helixUser `json:"data"`
}

type helixUser struct {
	ID              string `json:"id"`
	Login           string `json:"login"`
	DisplayName     string `json:"display_name"`
	ProfileImageURL string `json:"profile_image_url"`
}

func uniqueNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func uniqueLowerNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || slices.Contains(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func credentialSafeUserError(action string, err error, oauthToken string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	credentials := TokenCredentials{OAuthToken: oauthToken}
	return errors.New(action + ": " + redactCredentials(err.Error(), credentials))
}

// token returns the OAuth token to present on the next request, read now
// rather than captured at construction so a mid-session refresh applies.
func (c *HelixUsersClient) token() string {
	if c == nil || c.oauthTokenSource == nil {
		return ""
	}
	return c.oauthTokenSource()
}
