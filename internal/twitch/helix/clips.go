package helix

import (
	"context"
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

	decoded, err := sendJSON[helixCreateClipResponse](ctx, c.transport, http.MethodPost, c.endpoint,
		helixCreateClipRequest{BroadcasterID: broadcasterID},
		writeLabels{
			encodeAction: "encode Twitch clip request",
			createAction: "create Twitch clip request",
			decodeAction: "decode Twitch clip response",
			errorLabels:  clipErrorLabels("create Twitch clip", "Create Clip"),
		})
	if err != nil {
		return twitch.Clip{}, err
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
