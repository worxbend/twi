package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHelixStreamMarkersURL = "https://api.twitch.tv/helix/streams/markers"
	defaultStreamMarkerListLimit = 20
	maxStreamMarkerListLimit     = 100
)

// StreamMarker is one marked moment in a broadcaster's stream/VOD, created
// via Twitch Helix "Create Stream Marker" and listed via "Get Stream
// Markers".
type StreamMarker struct {
	ID              string
	CreatedAt       time.Time
	Description     string
	PositionSeconds int
	URL             string
}

// MarkerManager creates and lists Twitch stream markers for the
// broadcaster's own active stream. Creating a marker only succeeds while
// that broadcaster is live.
type MarkerManager interface {
	CreateStreamMarker(ctx context.Context, userID, description string) (StreamMarker, error)
	GetStreamMarkers(ctx context.Context, userID string, limit int) ([]StreamMarker, error)
}

// HelixMarkersClientConfig configures the Twitch Helix stream markers
// adapter. Endpoint and HTTPClient are injectable for deterministic fake
// HTTP tests; zero values use Twitch's production endpoint and the default
// HTTP client.
type HelixMarkersClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource func() string
	Endpoint         string
	HTTPClient       *http.Client
	ClientID         string
	OAuthToken       string
}

// HelixMarkersClient creates and lists stream markers through Twitch Helix
// "Create/Get Stream Markers".
type HelixMarkersClient struct {
	endpoint         string
	httpClient       *http.Client
	clientID         string
	oauthTokenSource func() string
}

var _ MarkerManager = (*HelixMarkersClient)(nil)

// NewHelixMarkersClient creates a MarkerManager backed by Twitch Helix HTTP.
// The returned client performs no network I/O until a method is called.
func NewHelixMarkersClient(cfg HelixMarkersClientConfig) *HelixMarkersClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultHelixStreamMarkersURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixMarkersClient{
		endpoint:         endpoint,
		httpClient:       httpClient,
		clientID:         strings.TrimSpace(cfg.ClientID),
		oauthTokenSource: resolveTokenSource(cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// CreateStreamMarker marks the current moment in userID's active stream.
// Twitch rejects this (with a 400) when the broadcaster is not currently
// live; description is optional and truncated by Twitch at 140 characters.
func (c *HelixMarkersClient) CreateStreamMarker(ctx context.Context, userID, description string) (StreamMarker, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return StreamMarker{}, fmt.Errorf("create Twitch stream marker: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return StreamMarker{}, err
	}

	body, err := json.Marshal(helixCreateMarkerRequest{UserID: userID, Description: strings.TrimSpace(description)})
	if err != nil {
		return StreamMarker{}, credentialSafeUserError("encode Twitch stream marker request", err, c.token())
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return StreamMarker{}, credentialSafeUserError("create Twitch stream marker request", err, c.token())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return StreamMarker{}, credentialSafeUserError("create Twitch stream marker", err, c.token())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return StreamMarker{}, c.responseError(resp, "create Twitch stream marker", "Create Stream Marker")
	}

	var decoded helixCreateMarkerResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return StreamMarker{}, credentialSafeUserError("decode Twitch stream marker response", err, c.token())
	}
	if len(decoded.Data) == 0 {
		return StreamMarker{}, fmt.Errorf("create Twitch stream marker: empty response")
	}
	return toStreamMarker(decoded.Data[0]), nil
}

// GetStreamMarkers lists markers for userID's most recent video (the
// current live broadcast's in-progress VOD, when live), most recent video
// first, in the order Twitch created the markers.
func (c *HelixMarkersClient) GetStreamMarkers(ctx context.Context, userID string, limit int) ([]StreamMarker, error) {
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.responseError(resp, "get Twitch stream markers", "Get Stream Markers")
	}

	var decoded helixStreamMarkersListResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, credentialSafeUserError("decode Twitch stream markers response", err, c.token())
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Videos) == 0 {
		return nil, nil
	}
	videoMarkers := decoded.Data[0].Videos[0].Markers
	out := make([]StreamMarker, 0, len(videoMarkers))
	for _, m := range videoMarkers {
		out = append(out, toStreamMarker(m))
	}
	return out, nil
}

func (c *HelixMarkersClient) setAuthHeaders(req *http.Request) {
	if c.clientID != "" {
		req.Header.Set("Client-Id", c.clientID)
	}
	token := accessTokenForValidation(c.token())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func (c *HelixMarkersClient) responseError(resp *http.Response, action, endpointLabel string) error {
	detail, err := readSmallBody(resp.Body)
	if err != nil {
		return credentialSafeUserError("read Twitch stream markers response", err, c.token())
	}
	if detail != "" {
		detail = ": " + detail
	}
	wrapped := credentialSafeUserError(
		action,
		fmt.Errorf("twitch %s returned HTTP %d%s", endpointLabel, resp.StatusCode, detail),
		c.token(),
	)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound {
		return &ChannelAPIError{StatusCode: resp.StatusCode, err: wrapped}
	}
	return wrapped
}

// IsNoVideoFound reports whether err is a 404 from Get Stream Markers,
// Twitch's response when the broadcaster has no video/VOD at all yet (never
// streamed to completion, or VOD storage is disabled) - not a real failure,
// just "there are no markers because there's nothing to attach them to".
func IsNoVideoFound(err error) bool {
	var apiErr *ChannelAPIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func toStreamMarker(m helixMarker) StreamMarker {
	createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
	return StreamMarker{
		ID:              strings.TrimSpace(m.ID),
		CreatedAt:       createdAt,
		Description:     m.Description,
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

// token returns the OAuth token to present on the next request, read now
// rather than captured at construction so a mid-session refresh applies.
func (c *HelixMarkersClient) token() string {
	if c == nil || c.oauthTokenSource == nil {
		return ""
	}
	return c.oauthTokenSource()
}
