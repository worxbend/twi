package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	defaultHelixChannelFollowersURL = "https://api.twitch.tv/helix/channels/followers"
	defaultFollowersPageLimit       = 20
	maxFollowersPageLimit           = 100
)

// FollowersClient resolves follower data through Twitch Helix "Get
// Channel Followers".
type FollowersClient struct {
	endpoint string
	transport
}

var _ twitch.FollowerLookup = (*FollowersClient)(nil)

// NewFollowersClient creates a FollowerLookup backed by Twitch Helix
// HTTP. The returned client performs no network I/O until
// GetChannelFollowers is called.
func NewFollowersClient(cfg FollowersClientConfig) *FollowersClient {
	return &FollowersClient{
		endpoint:  endpointOrDefault(cfg.Endpoint, defaultHelixChannelFollowersURL),
		transport: newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetChannelFollowers fetches broadcasterID's total follower count and its
// most recent followers (moderator_id is set to broadcasterID itself, the
// only case twi needs: a broadcaster reading their own followers).
func (c *FollowersClient) GetChannelFollowers(ctx context.Context, broadcasterID string, limit int) (twitch.FollowersPage, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return twitch.FollowersPage{}, fmt.Errorf("get Twitch channel followers: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return twitch.FollowersPage{}, err
	}
	if limit <= 0 {
		limit = defaultFollowersPageLimit
	}
	if limit > maxFollowersPageLimit {
		limit = maxFollowersPageLimit
	}

	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return twitch.FollowersPage{}, credentialSafeUserError("create Twitch channel followers request", err, c.token())
	}
	q := parsed.Query()
	q.Set("broadcaster_id", broadcasterID)
	q.Set("moderator_id", broadcasterID)
	q.Set("first", strconv.Itoa(limit))
	parsed.RawQuery = q.Encode()

	decoded, err := getJSON[helixFollowersResponse](ctx, c.transport, parsed.String(), errorLabels{
		action:     "get Twitch channel followers",
		readAction: "read Twitch channel followers response",
		endpoint:   "Get Channel Followers",

		channelAPIReasons: map[int]twitch.ChannelAPIReason{
			http.StatusUnauthorized: twitch.ChannelAPIMissingScope,
		},
	})
	if err != nil {
		return twitch.FollowersPage{}, err
	}
	followers := make([]twitch.Follower, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		followedAt, _ := time.Parse(time.RFC3339, item.FollowedAt)
		followers = append(followers, twitch.Follower{
			UserID:     strings.TrimSpace(item.UserID),
			UserLogin:  strings.TrimSpace(item.UserLogin),
			UserName:   textsafe.Display(strings.TrimSpace(item.UserName)),
			FollowedAt: followedAt,
		})
	}
	return twitch.FollowersPage{Total: decoded.Total, Followers: followers}, nil
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
