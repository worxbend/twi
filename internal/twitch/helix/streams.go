package helix

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

const defaultHelixStreamsURL = "https://api.twitch.tv/helix/streams"

// StreamsClient resolves broadcast status through Twitch Helix Get
// Streams.
type StreamsClient struct {
	endpoint string
	transport
}

var _ twitch.StreamLookup = (*StreamsClient)(nil)

// NewStreamsClient creates a StreamLookup backed by Twitch Helix HTTP.
// The returned client performs no network I/O until GetStreams is called.
func NewStreamsClient(cfg StreamsClientConfig) *StreamsClient {
	return &StreamsClient{
		endpoint:  endpointOrDefault(cfg.Endpoint, defaultHelixStreamsURL),
		transport: newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetStreams performs one Helix Get Streams request for the supplied unique
// logins and returns one StreamInfo per requested login, in the same order,
// including offline channels (which are simply absent from the Helix
// response). Empty requests return without network I/O.
func (c *StreamsClient) GetStreams(ctx context.Context, logins []string) ([]twitch.StreamInfo, error) {
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
	decoded, err := getJSON[helixStreamsResponse](ctx, c.transport, endpoint, errorLabels{
		action:     "lookup Twitch stream status",
		readAction: "read Twitch stream status response",
		endpoint:   "Get Streams",
	})
	if err != nil {
		return nil, err
	}

	live := make(map[string]helixStream, len(decoded.Data))
	for _, item := range decoded.Data {
		login := strings.ToLower(strings.TrimSpace(item.UserLogin))
		if login == "" {
			continue
		}
		live[login] = item
	}

	results := make([]twitch.StreamInfo, 0, len(unique))
	for _, login := range unique {
		item, ok := live[login]
		if !ok || strings.ToLower(strings.TrimSpace(item.Type)) != "live" {
			results = append(results, twitch.StreamInfo{UserLogin: login})
			continue
		}
		startedAt, _ := time.Parse(time.RFC3339, item.StartedAt)
		results = append(results, twitch.StreamInfo{
			UserLogin:   login,
			Live:        true,
			StartedAt:   startedAt,
			ViewerCount: item.ViewerCount,
		})
	}
	return results, nil
}

func (c *StreamsClient) streamsURL(logins []string) (string, error) {
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
