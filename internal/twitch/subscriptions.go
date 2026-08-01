package twitch

import (
	"context"
	"encoding/json"
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
	Endpoint   string
	HTTPClient *http.Client
	ClientID   string
	OAuthToken string
}

// HelixSubscriptionsClient resolves subscriber counts through Twitch Helix
// "Get Broadcaster Subscriptions".
type HelixSubscriptionsClient struct {
	endpoint   string
	httpClient *http.Client
	clientID   string
	oauthToken string
}

var _ SubscriptionLookup = (*HelixSubscriptionsClient)(nil)

// NewHelixSubscriptionsClient creates a SubscriptionLookup backed by Twitch
// Helix HTTP. The returned client performs no network I/O until
// GetBroadcasterSubscriptions is called.
func NewHelixSubscriptionsClient(cfg HelixSubscriptionsClientConfig) *HelixSubscriptionsClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultHelixSubscriptionsURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixSubscriptionsClient{
		endpoint:   endpoint,
		httpClient: httpClient,
		clientID:   strings.TrimSpace(cfg.ClientID),
		oauthToken: strings.TrimSpace(cfg.OAuthToken),
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
		return SubscriptionsPage{}, credentialSafeUserError("create Twitch broadcaster subscriptions request", err, c.oauthToken)
	}
	q := parsed.Query()
	q.Set("broadcaster_id", broadcasterID)
	q.Set("first", strconv.Itoa(limit))
	parsed.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return SubscriptionsPage{}, credentialSafeUserError("create Twitch broadcaster subscriptions request", err, c.oauthToken)
	}
	if c.clientID != "" {
		httpReq.Header.Set("Client-Id", c.clientID)
	}
	token := accessTokenForValidation(c.oauthToken)
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return SubscriptionsPage{}, credentialSafeUserError("get Twitch broadcaster subscriptions", err, c.oauthToken)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, readErr := readSmallBody(resp.Body)
		if readErr != nil {
			return SubscriptionsPage{}, credentialSafeUserError("read Twitch broadcaster subscriptions response", readErr, c.oauthToken)
		}
		if detail != "" {
			detail = ": " + detail
		}
		wrapped := credentialSafeUserError(
			"get Twitch broadcaster subscriptions",
			fmt.Errorf("twitch Get Broadcaster Subscriptions returned HTTP %d%s", resp.StatusCode, detail),
			c.oauthToken,
		)
		if resp.StatusCode == http.StatusUnauthorized {
			return SubscriptionsPage{}, &ChannelAPIError{StatusCode: resp.StatusCode, err: wrapped}
		}
		return SubscriptionsPage{}, wrapped
	}

	var decoded helixSubscriptionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return SubscriptionsPage{}, credentialSafeUserError("decode Twitch broadcaster subscriptions response", err, c.oauthToken)
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
