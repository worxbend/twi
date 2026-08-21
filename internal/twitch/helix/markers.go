package helix

import (
	"bytes"
	"context"
	"encoding/json"
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
	defaultHelixStreamMarkersURL = "https://api.twitch.tv/helix/streams/markers"
	defaultStreamMarkerListLimit = 20
	maxStreamMarkerListLimit     = 100
)

// MarkersClient creates and lists stream markers through Twitch Helix
// "Create/Get Stream Markers".
type MarkersClient struct {
	endpoint string
	transport
}

var _ twitch.MarkerManager = (*MarkersClient)(nil)

// NewMarkersClient creates a MarkerManager backed by Twitch Helix HTTP.
// The returned client performs no network I/O until a method is called.
func NewMarkersClient(cfg MarkersClientConfig) *MarkersClient {
	return &MarkersClient{
		endpoint:  endpointOrDefault(cfg.Endpoint, defaultHelixStreamMarkersURL),
		transport: newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// CreateStreamMarker marks the current moment in userID's active stream.
// Twitch rejects this (with a 400) when the broadcaster is not currently
// live; description is optional and truncated by Twitch at 140 characters.
func (c *MarkersClient) CreateStreamMarker(ctx context.Context, userID, description string) (twitch.StreamMarker, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return twitch.StreamMarker{}, fmt.Errorf("create Twitch stream marker: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return twitch.StreamMarker{}, err
	}

	body, err := json.Marshal(helixCreateMarkerRequest{UserID: userID, Description: strings.TrimSpace(description)})
	if err != nil {
		return twitch.StreamMarker{}, credentialSafeUserError("encode Twitch stream marker request", err, c.token())
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return twitch.StreamMarker{}, credentialSafeUserError("create Twitch stream marker request", err, c.token())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return twitch.StreamMarker{}, credentialSafeUserError("create Twitch stream marker", err, c.token())
	}
	defer resp.Body.Close()

	if !isSuccess(resp) {
		return twitch.StreamMarker{}, c.responseError(resp, markerErrorLabels("create Twitch stream marker", "Create Stream Marker"))
	}

	var decoded helixCreateMarkerResponse
	if err := decodeJSONBody(resp.Body, maxResponseBodySize, &decoded); err != nil {
		return twitch.StreamMarker{}, credentialSafeUserError("decode Twitch stream marker response", err, c.token())
	}
	if len(decoded.Data) == 0 {
		return twitch.StreamMarker{}, fmt.Errorf("create Twitch stream marker: empty response")
	}
	return toStreamMarker(decoded.Data[0]), nil
}

// GetStreamMarkers lists markers for userID's most recent video (the
// current live broadcast's in-progress VOD, when live), most recent video
// first, in the order Twitch created the markers.
func (c *MarkersClient) GetStreamMarkers(ctx context.Context, userID string, limit int) ([]twitch.StreamMarker, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("get Twitch stream markers: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultStreamMarkerListLimit
	}
	if limit > maxStreamMarkerListLimit {
		limit = maxStreamMarkerListLimit
	}

	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch stream markers request", err, c.token())
	}
	q := parsed.Query()
	q.Set("user_id", userID)
	q.Set("first", strconv.Itoa(limit))
	parsed.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch stream markers request", err, c.token())
	}
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, credentialSafeUserError("get Twitch stream markers", err, c.token())
	}
	defer resp.Body.Close()

	if !isSuccess(resp) {
		return nil, c.responseError(resp, markerErrorLabels("get Twitch stream markers", "Get Stream Markers"))
	}

	var decoded helixStreamMarkersListResponse
	if err := decodeJSONBody(resp.Body, maxResponseBodySize, &decoded); err != nil {
		return nil, credentialSafeUserError("decode Twitch stream markers response", err, c.token())
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Videos) == 0 {
		return nil, nil
	}
	videoMarkers := decoded.Data[0].Videos[0].Markers
	out := make([]twitch.StreamMarker, 0, len(videoMarkers))
	for _, m := range videoMarkers {
		out = append(out, toStreamMarker(m))
	}
	return out, nil
}

// markerErrorLabels describes a failure from this adapter's Helix calls. Twitch
// answers 401 for a token that is expired or missing the required scope and
// 404 for a broadcaster that is not live; callers tell those apart to explain
// the failure, so both arrive wrapped in a ChannelAPIError.
func markerErrorLabels(action, endpoint string) errorLabels {
	return errorLabels{
		action:     action,
		readAction: "read Twitch stream markers response",
		endpoint:   endpoint,
		channelAPIReasons: map[int]twitch.ChannelAPIReason{
			http.StatusUnauthorized: twitch.ChannelAPIMissingScope,
			http.StatusNotFound:     twitch.ChannelAPINoVideoFound,
		},
	}
}

func toStreamMarker(m helixMarker) twitch.StreamMarker {
	createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
	return twitch.StreamMarker{
		ID:              strings.TrimSpace(m.ID),
		CreatedAt:       createdAt,
		Description:     textsafe.Display(m.Description),
		PositionSeconds: m.PositionSeconds,
		URL:             strings.TrimSpace(m.URL),
	}
}

type helixCreateMarkerRequest struct {
	UserID      string `json:"user_id"`
	Description string `json:"description,omitempty"`
}

type helixCreateMarkerResponse struct {
	Data []helixMarker `json:"data"`
}

type helixMarker struct {
	ID              string `json:"id"`
	CreatedAt       string `json:"created_at"`
	Description     string `json:"description"`
	PositionSeconds int    `json:"position_seconds"`
	URL             string `json:"URL"`
}

type helixStreamMarkersListResponse struct {
	Data []helixStreamMarkersUser `json:"data"`
}

type helixStreamMarkersUser struct {
	UserID   string                    `json:"user_id"`
	UserName string                    `json:"user_name"`
	Videos   []helixStreamMarkersVideo `json:"videos"`
}

type helixStreamMarkersVideo struct {
	VideoID string        `json:"video_id"`
	Markers []helixMarker `json:"markers"`
}
