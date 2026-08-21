package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/worxbend/twi/internal/twitch"
)

const (
	defaultHelixSubscriptionsURL  = "https://api.twitch.tv/helix/subscriptions"
	defaultSubscriptionsPageLimit = 1
	maxSubscriptionsPageLimit     = 100
)

// SubscriptionsClient resolves subscriber counts through Twitch Helix
// "Get Broadcaster Subscriptions".
type SubscriptionsClient struct {
	endpoint string
	transport
}

var _ twitch.SubscriptionLookup = (*SubscriptionsClient)(nil)

// NewSubscriptionsClient creates a SubscriptionLookup backed by Twitch
// Helix HTTP. The returned client performs no network I/O until
// GetBroadcasterSubscriptions is called.
func NewSubscriptionsClient(cfg SubscriptionsClientConfig) *SubscriptionsClient {
	return &SubscriptionsClient{
		endpoint:  endpointOrDefault(cfg.Endpoint, defaultHelixSubscriptionsURL),
		transport: newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetBroadcasterSubscriptions fetches broadcasterID's subscriber count.
// Twitch returns `total`/`points` on every page regardless of how many
// subscriber rows are requested, so limit only bounds the (unused, for
// count purposes) row data and can stay small.
func (c *SubscriptionsClient) GetBroadcasterSubscriptions(ctx context.Context, broadcasterID string, limit int) (twitch.SubscriptionsPage, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return twitch.SubscriptionsPage{}, fmt.Errorf("get Twitch broadcaster subscriptions: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return twitch.SubscriptionsPage{}, err
	}
	if limit <= 0 {
		limit = defaultSubscriptionsPageLimit
	}
	if limit > maxSubscriptionsPageLimit {
		limit = maxSubscriptionsPageLimit
	}

	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return twitch.SubscriptionsPage{}, credentialSafeUserError("create Twitch broadcaster subscriptions request", err, c.token())
	}
	q := parsed.Query()
	q.Set("broadcaster_id", broadcasterID)
	q.Set("first", strconv.Itoa(limit))
	parsed.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return twitch.SubscriptionsPage{}, credentialSafeUserError("create Twitch broadcaster subscriptions request", err, c.token())
	}
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return twitch.SubscriptionsPage{}, credentialSafeUserError("get Twitch broadcaster subscriptions", err, c.token())
	}
	defer resp.Body.Close()

	if !isSuccess(resp) {
		return twitch.SubscriptionsPage{}, c.responseError(resp, errorLabels{
			action:     "get Twitch broadcaster subscriptions",
			readAction: "read Twitch broadcaster subscriptions response",
			endpoint:   "Get Broadcaster Subscriptions",

			channelAPIReasons: map[int]twitch.ChannelAPIReason{
				http.StatusUnauthorized: twitch.ChannelAPIMissingScope,
			},
		})
	}

	var decoded helixSubscriptionsResponse
	if err := decodeJSONBody(resp.Body, maxResponseBodySize, &decoded); err != nil {
		return twitch.SubscriptionsPage{}, credentialSafeUserError("decode Twitch broadcaster subscriptions response", err, c.token())
	}

	return twitch.SubscriptionsPage(decoded), nil
}

type helixSubscriptionsResponse struct {
	Total  int `json:"total"`
	Points int `json:"points"`
}
