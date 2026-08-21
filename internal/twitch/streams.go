package twitch

import (
	"context"
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

// HelixStreamsClient resolves broadcast status through Twitch Helix Get
// Streams.
type HelixStreamsClient struct {
	endpoint string
	helixTransport
}

var _ StreamLookup = (*HelixStreamsClient)(nil)

// NewHelixStreamsClient creates a StreamLookup backed by Twitch Helix HTTP.
// The returned client performs no network I/O until GetStreams is called.
func NewHelixStreamsClient(cfg HelixStreamsClientConfig) *HelixStreamsClient {
	return &HelixStreamsClient{
		endpoint:       endpointOrDefault(cfg.Endpoint, defaultHelixStreamsURL),
		helixTransport: newHelixTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
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
		return nil, credentialSafeUserError("create Twitch stream status request", err, c.token())
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch stream status request", err, c.token())
	}
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, credentialSafeUserError("lookup Twitch stream status", err, c.token())
	}
	defer resp.Body.Close()

	if !isHelixSuccess(resp) {
		return nil, c.responseError(resp, helixErrorLabels{
			action:     "lookup Twitch stream status",
			readAction: "read Twitch stream status response",
			endpoint:   "Get Streams",
		})
	}

	var decoded helixStreamsResponse
	if err := decodeJSONBody(resp.Body, maxHelixResponseBodySize, &decoded); err != nil {
		return nil, credentialSafeUserError("decode Twitch stream status response", err, c.token())
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
