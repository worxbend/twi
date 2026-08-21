package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
)

const defaultHelixChannelsURL = "https://api.twitch.tv/helix/channels"

// ChannelsClient reads and updates channel info through Twitch Helix
// "Get/Modify Channel Information".
type ChannelsClient struct {
	endpoint string
	transport
}

var _ twitch.ChannelManager = (*ChannelsClient)(nil)

// NewChannelsClient creates a ChannelManager backed by Twitch Helix
// HTTP. The returned client performs no network I/O until a method is
// called.
func NewChannelsClient(cfg ChannelsClientConfig) *ChannelsClient {
	return &ChannelsClient{
		endpoint:  endpointOrDefault(cfg.Endpoint, defaultHelixChannelsURL),
		transport: newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetChannelInformation performs one Helix Get Channel Information request
// for the given broadcaster ID.
func (c *ChannelsClient) GetChannelInformation(ctx context.Context, broadcasterID string) (twitch.ChannelInfo, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return twitch.ChannelInfo{}, fmt.Errorf("get Twitch channel information: missing broadcaster ID")
	}
	if err := ctx.Err(); err != nil {
		return twitch.ChannelInfo{}, err
	}

	endpoint, err := c.channelsURL(broadcasterID)
	if err != nil {
		return twitch.ChannelInfo{}, credentialSafeUserError("create Twitch channel information request", err, c.token())
	}
	decoded, err := getJSON[helixChannelsResponse](ctx, c.transport, endpoint, channelErrorLabels("get Twitch channel information", "Get Channel Information"))
	if err != nil {
		return twitch.ChannelInfo{}, err
	}
	if len(decoded.Data) == 0 {
		return twitch.ChannelInfo{}, fmt.Errorf("get Twitch channel information: no channel found for broadcaster")
	}
	item := decoded.Data[0]
	return twitch.ChannelInfo{
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
func (c *ChannelsClient) ModifyChannelInformation(ctx context.Context, broadcasterID string, update twitch.ChannelInfoUpdate) error {
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

	endpoint, err := c.channelsURL(broadcasterID)
	if err != nil {
		return credentialSafeUserError("create Twitch channel information update request", err, c.token())
	}
	return send(ctx, c.transport, http.MethodPatch, endpoint,
		helixChannelUpdateRequest{
			Title:               update.Title,
			GameID:              update.GameID,
			BroadcasterLanguage: update.Language,
			Tags:                update.Tags,
		},
		writeLabels{
			encodeAction: "encode Twitch channel information update",
			createAction: "create Twitch channel information update request",
			errorLabels:  channelErrorLabels("modify Twitch channel information", "Modify Channel Information"),
		})
}

// channelErrorLabels describes a Get/Modify Channel Information failure.
// Twitch answers 401 both for an expired token and for one missing
// channel:manage:broadcast, so that status is surfaced as a ChannelAPIError
// for callers that tell the user which login to re-run.
func channelErrorLabels(action, endpoint string) errorLabels {
	return errorLabels{
		action:     action,
		readAction: "read Twitch channel information response",
		endpoint:   endpoint,
		channelAPIReasons: map[int]twitch.ChannelAPIReason{
			http.StatusUnauthorized: twitch.ChannelAPIMissingScope,
		},
	}
}

// channelsURL points this adapter's endpoint at one broadcaster, which is the
// only query parameter both Get and Modify Channel Information take.
func (c *ChannelsClient) channelsURL(broadcasterID string) (string, error) {
	return queryURL(c.endpoint, url.Values{"broadcaster_id": {broadcasterID}})
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
