package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const defaultHelixClipsURL = "https://api.twitch.tv/helix/clips"

// Clip is the result of Twitch Helix "Create Clip". Twitch captures
// approximately the last 30-60 seconds of the broadcaster's stream at the
// moment of the call - there is no API parameter for a start time, end time,
// or duration. EditURL opens Twitch's own clip editor, where a human can
// trim the clip within whatever window Twitch still allows.
type Clip struct {
	ID      string
	EditURL string
}

// ClipManager creates a Twitch clip of the broadcaster's current stream.
// Twitch only accepts this call while the broadcaster is live.
type ClipManager interface {
	CreateClip(ctx context.Context, broadcasterID string) (Clip, error)
}

// HelixClipsClientConfig configures the Twitch Helix clips adapter. Endpoint
// and HTTPClient are injectable for deterministic fake HTTP tests; zero
// values use Twitch's production endpoint and the default HTTP client.
type HelixClipsClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource func() string
	Endpoint         string
	HTTPClient       *http.Client
	ClientID         string
	OAuthToken       string
}

// HelixClipsClient creates clips through Twitch Helix "Create Clip".
type HelixClipsClient struct {
	endpoint string
	helixTransport
}

var _ ClipManager = (*HelixClipsClient)(nil)

// NewHelixClipsClient creates a ClipManager backed by Twitch Helix HTTP. The
// returned client performs no network I/O until CreateClip is called.
func NewHelixClipsClient(cfg HelixClipsClientConfig) *HelixClipsClient {
	return &HelixClipsClient{
		endpoint:       endpointOrDefault(cfg.Endpoint, defaultHelixClipsURL),
		helixTransport: newHelixTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// CreateClip captures a clip of broadcasterID's current stream. Twitch
// rejects this (with a 404) when the broadcaster is not currently live.
func (c *HelixClipsClient) CreateClip(ctx context.Context, broadcasterID string) (Clip, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return Clip{}, fmt.Errorf("create Twitch clip: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return Clip{}, err
	}

	body, err := json.Marshal(helixCreateClipRequest{BroadcasterID: broadcasterID})
	if err != nil {
		return Clip{}, credentialSafeUserError("encode Twitch clip request", err, c.token())
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Clip{}, credentialSafeUserError("create Twitch clip request", err, c.token())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Clip{}, credentialSafeUserError("create Twitch clip", err, c.token())
	}
	defer resp.Body.Close()

	if !isHelixSuccess(resp) {
		return Clip{}, c.responseError(resp, clipErrorLabels("create Twitch clip", "Create Clip"))
	}

	var decoded helixCreateClipResponse
	if err := decodeJSONBody(resp.Body, maxHelixResponseBodySize, &decoded); err != nil {
		return Clip{}, credentialSafeUserError("decode Twitch clip response", err, c.token())
	}
	if len(decoded.Data) == 0 {
		return Clip{}, fmt.Errorf("create Twitch clip: empty response")
	}
	return Clip{
		ID:      strings.TrimSpace(decoded.Data[0].ID),
		EditURL: strings.TrimSpace(decoded.Data[0].EditURL),
	}, nil
}

// clipErrorLabels describes a failure from this adapter's Helix calls. Twitch
// answers 401 for a token that is expired or missing the required scope and
// 404 for a broadcaster that is not live; callers tell those apart to explain
// the failure, so both arrive wrapped in a ChannelAPIError.
func clipErrorLabels(action, endpoint string) helixErrorLabels {
	return helixErrorLabels{
		action:             action,
		readAction:         "read Twitch clip response",
		endpoint:           endpoint,
		channelAPIStatuses: []int{http.StatusUnauthorized, http.StatusNotFound},
	}
}

// IsClipCreationUnavailable reports whether err is a 404 from Create Clip,
// Twitch's response when the broadcaster isn't currently live (clips can
// only be cut from a live broadcast).
func IsClipCreationUnavailable(err error) bool {
	return IsNoVideoFound(err)
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
