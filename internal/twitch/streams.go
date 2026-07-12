package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultHelixStreamsURL = "https://api.twitch.tv/helix/streams"

// StreamInfo reports Twitch Helix "Get Streams" broadcast status for one
// channel. A channel absent from the Helix response is offline: Live is
// false and StartedAt/ViewerCount are zero.
type StreamInfo struct {
	UserLogin   string
	Live        bool
	StartedAt   time.Time
	ViewerCount int
}

// StreamLookup resolves live broadcast status for a batch of channel logins.
type StreamLookup interface {
	GetStreams(ctx context.Context, logins []string) ([]StreamInfo, error)
}

// HelixStreamsClientConfig configures the Twitch Helix Get Streams adapter.
// Endpoint, HTTPClient are injectable for deterministic fake HTTP tests; zero
// values use Twitch's production endpoint and the default HTTP client.
type HelixStreamsClientConfig struct {
	Endpoint   string
	HTTPClient *http.Client
	ClientID   string
	OAuthToken string
}

// HelixStreamsClient resolves broadcast status through Twitch Helix Get
// Streams.
type HelixStreamsClient struct {
	endpoint   string
	httpClient *http.Client
	clientID   string
	oauthToken string
}

var _ StreamLookup = (*HelixStreamsClient)(nil)

// NewHelixStreamsClient creates a StreamLookup backed by Twitch Helix HTTP.
// The returned client performs no network I/O until GetStreams is called.
func NewHelixStreamsClient(cfg HelixStreamsClientConfig) *HelixStreamsClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultHelixStreamsURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixStreamsClient{
		endpoint:   endpoint,
		httpClient: httpClient,
		clientID:   strings.TrimSpace(cfg.ClientID),
		oauthToken: strings.TrimSpace(cfg.OAuthToken),
	}
}

// GetStreams performs one Helix Get Streams request for the supplied unique
// logins and returns one StreamInfo per requested login, in the same order,
// including offline channels (which are simply absent from the Helix
// response). Empty requests return without network I/O.
func (c *HelixStreamsClient) GetStreams(ctx context.Context, logins []string) ([]StreamInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unique := uniqueLowerNonEmpty(logins)
	if len(unique) == 0 {
		return nil, nil
	}

	endpoint, err := c.streamsURL(unique)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch stream status request", err, c.oauthToken)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch stream status request", err, c.oauthToken)
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
		return nil, credentialSafeUserError("lookup Twitch stream status", err, c.oauthToken)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, err := readSmallBody(resp.Body)
		if err != nil {
			return nil, credentialSafeUserError("read Twitch stream status response", err, c.oauthToken)
		}
		if detail != "" {
			detail = ": " + detail
		}
		return nil, credentialSafeUserError(
			"lookup Twitch stream status",
			fmt.Errorf("twitch Get Streams returned HTTP %d%s", resp.StatusCode, detail),
			c.oauthToken,
		)
	}

	var decoded helixStreamsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, credentialSafeUserError("decode Twitch stream status response", err, c.oauthToken)
	}

	live := make(map[string]helixStream, len(decoded.Data))
	for _, item := range decoded.Data {
		login := strings.ToLower(strings.TrimSpace(item.UserLogin))
		if login == "" {
			continue
		}
		live[login] = item
	}

	results := make([]StreamInfo, 0, len(unique))
	for _, login := range unique {
		item, ok := live[login]
		if !ok || strings.ToLower(strings.TrimSpace(item.Type)) != "live" {
			results = append(results, StreamInfo{UserLogin: login})
			continue
		}
		startedAt, _ := time.Parse(time.RFC3339, item.StartedAt)
		results = append(results, StreamInfo{
			UserLogin:   login,
			Live:        true,
			StartedAt:   startedAt,
			ViewerCount: item.ViewerCount,
		})
	}
	return results, nil
}

func (c *HelixStreamsClient) streamsURL(logins []string) (string, error) {
	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for _, login := range logins {
		query.Add("user_login", login)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type helixStreamsResponse struct {
	Data []helixStream `json:"data"`
}

type helixStream struct {
	UserLogin   string `json:"user_login"`
	Type        string `json:"type"`
	StartedAt   string `json:"started_at"`
	ViewerCount int    `json:"viewer_count"`
}
