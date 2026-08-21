package helix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/worxbend/twi/internal/twitch"
)

const defaultHelixClipsURL = "https://api.twitch.tv/helix/clips"

// ClipsClient creates clips through Twitch Helix "Create Clip".
type ClipsClient struct {
	endpoint string
	transport
}

var _ twitch.ClipManager = (*ClipsClient)(nil)

// NewClipsClient creates a ClipManager backed by Twitch Helix HTTP. The
// returned client performs no network I/O until CreateClip is called.
func NewClipsClient(cfg ClipsClientConfig) *ClipsClient {
	return &ClipsClient{
		endpoint:  endpointOrDefault(cfg.Endpoint, defaultHelixClipsURL),
		transport: newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// CreateClip captures a clip of broadcasterID's current stream. Twitch
// rejects this (with a 404) when the broadcaster is not currently live.
func (c *ClipsClient) CreateClip(ctx context.Context, broadcasterID string) (twitch.Clip, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return twitch.Clip{}, fmt.Errorf("create Twitch clip: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return twitch.Clip{}, err
	}

	body, err := json.Marshal(helixCreateClipRequest{BroadcasterID: broadcasterID})
	if err != nil {
		return twitch.Clip{}, credentialSafeUserError("encode Twitch clip request", err, c.token())
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return twitch.Clip{}, credentialSafeUserError("create Twitch clip request", err, c.token())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return twitch.Clip{}, credentialSafeUserError("create Twitch clip", err, c.token())
	}
	defer resp.Body.Close()

	if !isSuccess(resp) {
		return twitch.Clip{}, c.responseError(resp, clipErrorLabels("create Twitch clip", "Create Clip"))
	}

	var decoded helixCreateClipResponse
	if err := decodeJSONBody(resp.Body, maxResponseBodySize, &decoded); err != nil {
		return twitch.Clip{}, credentialSafeUserError("decode Twitch clip response", err, c.token())
	}
	if len(decoded.Data) == 0 {
		return twitch.Clip{}, fmt.Errorf("create Twitch clip: empty response")
	}
	return twitch.Clip{
		ID:      strings.TrimSpace(decoded.Data[0].ID),
		EditURL: strings.TrimSpace(decoded.Data[0].EditURL),
	}, nil
}

// clipErrorLabels describes a failure from this adapter's Helix calls. Twitch
// answers 401 for a token that is expired or missing the required scope and
// 404 for a broadcaster that is not live; callers tell those apart to explain
// the failure, so both arrive wrapped in a ChannelAPIError.
func clipErrorLabels(action, endpoint string) errorLabels {
	return errorLabels{
		action:     action,
		readAction: "read Twitch clip response",
		endpoint:   endpoint,
		channelAPIReasons: map[int]twitch.ChannelAPIReason{
			http.StatusUnauthorized: twitch.ChannelAPIMissingScope,
			http.StatusNotFound:     twitch.ChannelAPINoVideoFound,
		},
	}
}

type helixCreateClipRequest struct {
	BroadcasterID string `json:"broadcaster_id"`
}

type helixCreateClipResponse struct {
	Data []helixClip `json:"data"`
}

type helixClip struct {
	ID      string `json:"id"`
	EditURL string `json:"edit_url"`
}
