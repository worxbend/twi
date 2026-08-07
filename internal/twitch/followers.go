package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHelixChannelFollowersURL = "https://api.twitch.tv/helix/channels/followers"
	defaultFollowersPageLimit       = 20
	maxFollowersPageLimit           = 100
)

// Follower is one of a broadcaster's followers, as reported by Twitch Helix
// "Get Channel Followers" (sorted most-recently-followed first).
type Follower struct {
	UserID     string
	UserLogin  string
	UserName   string
	FollowedAt time.Time
}

// FollowersPage is one page of Twitch Helix "Get Channel Followers" results:
// the full follower count plus the most recent followers (for detecting new
// ones by polling and diffing, since Twitch only offers follow events
// through EventSub, not IRC or a webhook twi can receive).
type FollowersPage struct {
	Total     int
	Followers []Follower
}

// FollowerLookup resolves a broadcaster's follower count and most recent
// followers. Requires the moderator:read:followers scope, granted by the
// broadcaster to themselves (moderator_id == broadcaster_id).
type FollowerLookup interface {
	GetChannelFollowers(ctx context.Context, broadcasterID string, limit int) (FollowersPage, error)
}

// HelixFollowersClientConfig configures the Twitch Helix channel followers
// adapter. Endpoint and HTTPClient are injectable for deterministic fake
// HTTP tests; zero values use Twitch's production endpoint and the default
// HTTP client.
type HelixFollowersClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource func() string
	Endpoint         string
	HTTPClient       *http.Client
	ClientID         string
	OAuthToken       string
}

// HelixFollowersClient resolves follower data through Twitch Helix "Get
// Channel Followers".
type HelixFollowersClient struct {
	endpoint         string
	httpClient       *http.Client
	clientID         string
	oauthTokenSource func() string
}

var _ FollowerLookup = (*HelixFollowersClient)(nil)

// NewHelixFollowersClient creates a FollowerLookup backed by Twitch Helix
// HTTP. The returned client performs no network I/O until
// GetChannelFollowers is called.
func NewHelixFollowersClient(cfg HelixFollowersClientConfig) *HelixFollowersClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultHelixChannelFollowersURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixFollowersClient{
		endpoint:         endpoint,
		httpClient:       httpClient,
		clientID:         strings.TrimSpace(cfg.ClientID),
		oauthTokenSource: resolveTokenSource(cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetChannelFollowers fetches broadcasterID's total follower count and its
// most recent followers (moderator_id is set to broadcasterID itself, the
// only case twi needs: a broadcaster reading their own followers).
func (c *HelixFollowersClient) GetChannelFollowers(ctx context.Context, broadcasterID string, limit int) (FollowersPage, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return FollowersPage{}, fmt.Errorf("get Twitch channel followers: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return FollowersPage{}, err
	}
	if limit <= 0 {
		limit = defaultFollowersPageLimit
	}
	if limit > maxFollowersPageLimit {
		limit = maxFollowersPageLimit
	}

	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return FollowersPage{}, credentialSafeUserError("create Twitch channel followers request", err, c.token())
	}
	q := parsed.Query()
	q.Set("broadcaster_id", broadcasterID)
	q.Set("moderator_id", broadcasterID)
	q.Set("first", strconv.Itoa(limit))
	parsed.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return FollowersPage{}, credentialSafeUserError("create Twitch channel followers request", err, c.token())
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
		return FollowersPage{}, credentialSafeUserError("get Twitch channel followers", err, c.token())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, readErr := readSmallBody(resp.Body)
		if readErr != nil {
			return FollowersPage{}, credentialSafeUserError("read Twitch channel followers response", readErr, c.token())
		}
		if detail != "" {
			detail = ": " + detail
		}
		wrapped := credentialSafeUserError(
			"get Twitch channel followers",
			fmt.Errorf("twitch Get Channel Followers returned HTTP %d%s", resp.StatusCode, detail),
			c.token(),
		)
		if resp.StatusCode == http.StatusUnauthorized {
			return FollowersPage{}, &ChannelAPIError{StatusCode: resp.StatusCode, err: wrapped}
		}
		return FollowersPage{}, wrapped
	}

	var decoded helixFollowersResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return FollowersPage{}, credentialSafeUserError("decode Twitch channel followers response", err, c.token())
	}
	followers := make([]Follower, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		followedAt, _ := time.Parse(time.RFC3339, item.FollowedAt)
		followers = append(followers, Follower{
			UserID:     strings.TrimSpace(item.UserID),
			UserLogin:  strings.TrimSpace(item.UserLogin),
			UserName:   strings.TrimSpace(item.UserName),
			FollowedAt: followedAt,
		})
	}
	return FollowersPage{Total: decoded.Total, Followers: followers}, nil
}

type helixFollowersResponse struct {
	Total int                 `json:"total"`
	Data  []helixFollowerItem `json:"data"`
}

type helixFollowerItem struct {
	UserID     string `json:"user_id"`
	UserLogin  string `json:"user_login"`
	UserName   string `json:"user_name"`
	FollowedAt string `json:"followed_at"`
}

// token returns the OAuth token to present on the next request, read now
// rather than captured at construction so a mid-session refresh applies.
func (c *HelixFollowersClient) token() string {
	if c == nil || c.oauthTokenSource == nil {
		return ""
	}
	return c.oauthTokenSource()
}
