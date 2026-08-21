package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	defaultHelixSubscriptionsURL  = "https://api.twitch.tv/helix/subscriptions"
	defaultSubscriptionsPageLimit = 1
	maxSubscriptionsPageLimit     = 100
)

// SubscriptionsPage reports a broadcaster's subscriber count from Twitch
// Helix "Get Broadcaster Subscriptions". Points is Twitch's weighted total
// (Tier 2/3 subs count as more than one point); Total is the plain
// subscriber count.
type SubscriptionsPage struct {
	Total  int
	Points int
}

// SubscriptionLookup resolves a broadcaster's subscriber count. Requires the
// channel:read:subscriptions scope.
type SubscriptionLookup interface {
	GetBroadcasterSubscriptions(ctx context.Context, broadcasterID string, limit int) (SubscriptionsPage, error)
}

// HelixSubscriptionsClientConfig configures the Twitch Helix broadcaster
// subscriptions adapter. Endpoint and HTTPClient are injectable for
// deterministic fake HTTP tests; zero values use Twitch's production
// endpoint and the default HTTP client.
type HelixSubscriptionsClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource func() string
	Endpoint         string
	HTTPClient       *http.Client
	ClientID         string
	OAuthToken       string
}

// HelixSubscriptionsClient resolves subscriber counts through Twitch Helix
// "Get Broadcaster Subscriptions".
type HelixSubscriptionsClient struct {
	endpoint string
	helixTransport
}

var _ SubscriptionLookup = (*HelixSubscriptionsClient)(nil)

// NewHelixSubscriptionsClient creates a SubscriptionLookup backed by Twitch
// Helix HTTP. The returned client performs no network I/O until
// GetBroadcasterSubscriptions is called.
func NewHelixSubscriptionsClient(cfg HelixSubscriptionsClientConfig) *HelixSubscriptionsClient {
	return &HelixSubscriptionsClient{
		endpoint:       endpointOrDefault(cfg.Endpoint, defaultHelixSubscriptionsURL),
		helixTransport: newHelixTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetBroadcasterSubscriptions fetches broadcasterID's subscriber count.
// Twitch returns `total`/`points` on every page regardless of how many
// subscriber rows are requested, so limit only bounds the (unused, for
// count purposes) row data and can stay small.
func (c *HelixSubscriptionsClient) GetBroadcasterSubscriptions(ctx context.Context, broadcasterID string, limit int) (SubscriptionsPage, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return SubscriptionsPage{}, fmt.Errorf("get Twitch broadcaster subscriptions: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return SubscriptionsPage{}, err
	}
	if limit <= 0 {
		limit = defaultSubscriptionsPageLimit
	}
	if limit > maxSubscriptionsPageLimit {
		limit = maxSubscriptionsPageLimit
	}

	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return SubscriptionsPage{}, credentialSafeUserError("create Twitch broadcaster subscriptions request", err, c.token())
	}
	q := parsed.Query()
	q.Set("broadcaster_id", broadcasterID)
	q.Set("first", strconv.Itoa(limit))
	parsed.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return SubscriptionsPage{}, credentialSafeUserError("create Twitch broadcaster subscriptions request", err, c.token())
	}
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return SubscriptionsPage{}, credentialSafeUserError("get Twitch broadcaster subscriptions", err, c.token())
	}
	defer resp.Body.Close()

	if !isHelixSuccess(resp) {
		return SubscriptionsPage{}, c.responseError(resp, helixErrorLabels{
			action:     "get Twitch broadcaster subscriptions",
			readAction: "read Twitch broadcaster subscriptions response",
			endpoint:   "Get Broadcaster Subscriptions",
			// A token missing channel:read:subscriptions fails with 401,
			// which IsMissingScope recognizes, so the caller can name the
			// scope instead of reporting a generic failure.
			channelAPIStatuses: []int{http.StatusUnauthorized},
		})
	}

	var decoded helixSubscriptionsResponse
	if err := decodeJSONBody(resp.Body, maxHelixResponseBodySize, &decoded); err != nil {
		return SubscriptionsPage{}, credentialSafeUserError("decode Twitch broadcaster subscriptions response", err, c.token())
	}
	// The wire struct and SubscriptionsPage carry the same fields, so a
	// conversion keeps them in lockstep: adding a field to one without the
	// other becomes a compile error rather than a silently dropped value.
	return SubscriptionsPage(decoded), nil
}

type helixSubscriptionsResponse struct {
	Total  int `json:"total"`
	Points int `json:"points"`
}
