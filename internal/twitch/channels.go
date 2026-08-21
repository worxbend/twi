package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/worxbend/twi/internal/textsafe"
)

const defaultHelixChannelsURL = "https://api.twitch.tv/helix/channels"

// ChannelInfo reports Twitch Helix "Get Channel Information" fields for one
// broadcaster.
type ChannelInfo struct {
	BroadcasterID    string
	BroadcasterLogin string
	BroadcasterName  string
	GameID           string
	GameName         string
	Title            string
	Language         string
	Tags             []string
}

// ChannelInfoUpdate describes a Twitch Helix "Modify Channel Information"
// request. Only non-nil fields are sent, so unrelated channel info is left
// untouched; Tags, when non-nil, replaces the full tag list (Twitch has no
// partial-tag-update endpoint).
type ChannelInfoUpdate struct {
	Title    *string
	GameID   *string
	Language *string
	Tags     *[]string
}

// IsEmpty reports whether the update has no fields set, so callers can skip
// issuing a no-op PATCH request.
func (u ChannelInfoUpdate) IsEmpty() bool {
	return u.Title == nil && u.GameID == nil && u.Language == nil && u.Tags == nil
}

// ChannelManager resolves and updates a broadcaster's own channel info
// through Twitch Helix. Implementations must not perform network work from a
// Bubble Tea View.
type ChannelManager interface {
	GetChannelInformation(ctx context.Context, broadcasterID string) (ChannelInfo, error)
	ModifyChannelInformation(ctx context.Context, broadcasterID string, update ChannelInfoUpdate) error
}

// HelixChannelsClient reads and updates channel info through Twitch Helix
// "Get/Modify Channel Information".
type HelixChannelsClient struct {
	endpoint string
	helixTransport
}

var _ ChannelManager = (*HelixChannelsClient)(nil)

// NewHelixChannelsClient creates a ChannelManager backed by Twitch Helix
// HTTP. The returned client performs no network I/O until a method is
// called.
func NewHelixChannelsClient(cfg HelixChannelsClientConfig) *HelixChannelsClient {
	return &HelixChannelsClient{
		endpoint:       endpointOrDefault(cfg.Endpoint, defaultHelixChannelsURL),
		helixTransport: newHelixTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetChannelInformation performs one Helix Get Channel Information request
// for the given broadcaster ID.
func (c *HelixChannelsClient) GetChannelInformation(ctx context.Context, broadcasterID string) (ChannelInfo, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return ChannelInfo{}, fmt.Errorf("get Twitch channel information: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return ChannelInfo{}, err
	}

	endpoint, err := c.channelsURL(broadcasterID)
	if err != nil {
		return ChannelInfo{}, credentialSafeUserError("create Twitch channel information request", err, c.token())
	}
	httpReq, err := c.newGetRequest(ctx, endpoint, "create Twitch channel information request")
	if err != nil {
		return ChannelInfo{}, err
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ChannelInfo{}, credentialSafeUserError("get Twitch channel information", err, c.token())
	}
	defer resp.Body.Close()

	if !isHelixSuccess(resp) {
		return ChannelInfo{}, c.responseError(resp, channelErrorLabels("get Twitch channel information", "Get Channel Information"))
	}

	var decoded helixChannelsResponse
	if err := decodeJSONBody(resp.Body, maxHelixResponseBodySize, &decoded); err != nil {
		return ChannelInfo{}, credentialSafeUserError("decode Twitch channel information response", err, c.token())
	}
	if len(decoded.Data) == 0 {
		return ChannelInfo{}, fmt.Errorf("get Twitch channel information: no channel found for broadcaster")
	}
	item := decoded.Data[0]
	return ChannelInfo{
		BroadcasterID:    strings.TrimSpace(item.BroadcasterID),
		BroadcasterLogin: strings.TrimSpace(item.BroadcasterLogin),
		BroadcasterName:  textsafe.Display(strings.TrimSpace(item.BroadcasterName)),
		GameID:           strings.TrimSpace(item.GameID),
		GameName:         textsafe.Display(strings.TrimSpace(item.GameName)),
		Title:            textsafe.Display(item.Title),
		Language:         strings.TrimSpace(item.BroadcasterLanguage),
		Tags:             sanitizeDisplayList(item.Tags),
	}, nil
}

// ModifyChannelInformation performs one Helix Modify Channel Information
// request, sending only the fields set on update. A successful request
// returns Twitch's 204 No Content response as a nil error.
func (c *HelixChannelsClient) ModifyChannelInformation(ctx context.Context, broadcasterID string, update ChannelInfoUpdate) error {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return fmt.Errorf("modify Twitch channel information: missing broadcaster ID")
	}
	if update.IsEmpty() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	body, err := json.Marshal(helixChannelUpdateRequest{
		Title:               update.Title,
		GameID:              update.GameID,
		BroadcasterLanguage: update.Language,
		Tags:                update.Tags,
	})
	if err != nil {
		return credentialSafeUserError("encode Twitch channel information update", err, c.token())
	}

	endpoint, err := c.channelsURL(broadcasterID)
	if err != nil {
		return credentialSafeUserError("create Twitch channel information update request", err, c.token())
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return credentialSafeUserError("create Twitch channel information update request", err, c.token())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return credentialSafeUserError("modify Twitch channel information", err, c.token())
	}
	defer resp.Body.Close()

	if !isHelixSuccess(resp) {
		return c.responseError(resp, channelErrorLabels("modify Twitch channel information", "Modify Channel Information"))
	}
	return nil
}

// channelErrorLabels describes a Get/Modify Channel Information failure.
// Twitch answers 401 both for an expired token and for one missing
// channel:manage:broadcast, so that status is surfaced as a ChannelAPIError
// for callers that tell the user which login to re-run.
func channelErrorLabels(action, endpoint string) helixErrorLabels {
	return helixErrorLabels{
		action:             action,
		readAction:         "read Twitch channel information response",
		endpoint:           endpoint,
		channelAPIStatuses: []int{http.StatusUnauthorized},
	}
}

// ChannelAPIError wraps a Get/Modify Channel Information failure with the
// HTTP status Twitch returned, so callers can distinguish "missing scope or
// unauthorized token" (401) from other failures without parsing message
// text. Twitch returns 401 for both an expired/invalid token and a token
// missing channel:manage:broadcast (or the older equivalent scopes), so
// IsMissingScope treats either case as "re-run `twi login`."
type ChannelAPIError struct {
	StatusCode int
	err        error
}

func (e *ChannelAPIError) Error() string { return e.err.Error() }

func (e *ChannelAPIError) Unwrap() error { return e.err }

// IsMissingScope reports whether err is a ChannelAPIError for an
// unauthorized (401) Twitch response - in practice always caused by the
// token lacking channel:manage:broadcast or having expired.
func IsMissingScope(err error) bool {
	var apiErr *ChannelAPIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized
}

func (c *HelixChannelsClient) channelsURL(broadcasterID string) (string, error) {
	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("broadcaster_id", broadcasterID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type helixChannelsResponse struct {
	Data []helixChannel `json:"data"`
}

type helixChannel struct {
	BroadcasterID       string   `json:"broadcaster_id"`
	BroadcasterLogin    string   `json:"broadcaster_login"`
	BroadcasterName     string   `json:"broadcaster_name"`
	GameID              string   `json:"game_id"`
	GameName            string   `json:"game_name"`
	Title               string   `json:"title"`
	BroadcasterLanguage string   `json:"broadcaster_language"`
	Tags                []string `json:"tags"`
}

type helixChannelUpdateRequest struct {
	Title               *string   `json:"title,omitempty"`
	GameID              *string   `json:"game_id,omitempty"`
	BroadcasterLanguage *string   `json:"broadcaster_language,omitempty"`
	Tags                *[]string `json:"tags,omitempty"`
}
