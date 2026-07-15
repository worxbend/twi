package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// HelixChannelsClientConfig configures the Twitch Helix channel info adapter.
// Endpoint and HTTPClient are injectable for deterministic fake HTTP tests;
// zero values use Twitch's production endpoint and the default HTTP client.
type HelixChannelsClientConfig struct {
	Endpoint   string
	HTTPClient *http.Client
	ClientID   string
	OAuthToken string
}

// HelixChannelsClient reads and updates channel info through Twitch Helix
// "Get/Modify Channel Information".
type HelixChannelsClient struct {
	endpoint   string
	httpClient *http.Client
	clientID   string
	oauthToken string
}

var _ ChannelManager = (*HelixChannelsClient)(nil)

// NewHelixChannelsClient creates a ChannelManager backed by Twitch Helix
// HTTP. The returned client performs no network I/O until a method is
// called.
func NewHelixChannelsClient(cfg HelixChannelsClientConfig) *HelixChannelsClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultHelixChannelsURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixChannelsClient{
		endpoint:   endpoint,
		httpClient: httpClient,
		clientID:   strings.TrimSpace(cfg.ClientID),
		oauthToken: strings.TrimSpace(cfg.OAuthToken),
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
		return ChannelInfo{}, credentialSafeUserError("create Twitch channel information request", err, c.oauthToken)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ChannelInfo{}, credentialSafeUserError("create Twitch channel information request", err, c.oauthToken)
	}
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ChannelInfo{}, credentialSafeUserError("get Twitch channel information", err, c.oauthToken)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChannelInfo{}, c.responseError(resp, "get Twitch channel information", "Get Channel Information")
	}

	var decoded helixChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return ChannelInfo{}, credentialSafeUserError("decode Twitch channel information response", err, c.oauthToken)
	}
	if len(decoded.Data) == 0 {
		return ChannelInfo{}, fmt.Errorf("get Twitch channel information: no channel found for broadcaster")
	}
	item := decoded.Data[0]
	return ChannelInfo{
		BroadcasterID:    strings.TrimSpace(item.BroadcasterID),
		BroadcasterLogin: strings.TrimSpace(item.BroadcasterLogin),
		BroadcasterName:  strings.TrimSpace(item.BroadcasterName),
		GameID:           strings.TrimSpace(item.GameID),
		GameName:         strings.TrimSpace(item.GameName),
		Title:            item.Title,
		Language:         strings.TrimSpace(item.BroadcasterLanguage),
		Tags:             append([]string(nil), item.Tags...),
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
		return credentialSafeUserError("encode Twitch channel information update", err, c.oauthToken)
	}

	endpoint, err := c.channelsURL(broadcasterID)
	if err != nil {
		return credentialSafeUserError("create Twitch channel information update request", err, c.oauthToken)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return credentialSafeUserError("create Twitch channel information update request", err, c.oauthToken)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return credentialSafeUserError("modify Twitch channel information", err, c.oauthToken)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.responseError(resp, "modify Twitch channel information", "Modify Channel Information")
	}
	return nil
}

func (c *HelixChannelsClient) setAuthHeaders(req *http.Request) {
	if c.clientID != "" {
		req.Header.Set("Client-Id", c.clientID)
	}
	token := accessTokenForValidation(c.oauthToken)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func (c *HelixChannelsClient) responseError(resp *http.Response, action, endpointLabel string) error {
	detail, err := readSmallBody(resp.Body)
	if err != nil {
		return credentialSafeUserError("read Twitch channel information response", err, c.oauthToken)
	}
	if detail != "" {
		detail = ": " + detail
	}
	return credentialSafeUserError(
		action,
		fmt.Errorf("twitch %s returned HTTP %d%s", endpointLabel, resp.StatusCode, detail),
		c.oauthToken,
	)
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
