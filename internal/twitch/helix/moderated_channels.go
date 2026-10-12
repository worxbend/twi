package helix

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
)

const maxModeratedChannelPages = 50

var ErrModeratorProofIncomplete = errors.New("moderated channel lookup exceeded pagination bound")

type ModeratedChannelsClient struct {
	transport
	endpoint string
}

var _ twitch.ModeratedChannelLookup = (*ModeratedChannelsClient)(nil)

func NewModeratedChannelsClient(config ClientConfig) *ModeratedChannelsClient {
	return &ModeratedChannelsClient{transport: newTransport(config.HTTPClient, config.ClientID, config.OAuthTokenSource, config.OAuthToken, config.RetryPolicy), endpoint: endpointOrDefault(config.Endpoint, "https://api.twitch.tv/helix/moderation/channels")}
}

// GetModeratedChannels proves positive selected IDs from current Twitch results.
// Pagination is bounded by 50 pages and the caller's context. If all selected
// IDs are found, no further pages are needed. An incomplete negative lookup is
// returned as an error, never mistaken for an authoritative complete list.
func (c *ModeratedChannelsClient) GetModeratedChannels(ctx context.Context, userID string, selected []string) ([]twitch.ModeratedChannel, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("moderated channel lookup requires token user ID")
	}
	if len(selected) == 0 || len(selected) > 100 {
		return nil, errors.New("moderated channel lookup requires 1 to 100 selected IDs")
	}
	wanted := map[string]bool{}
	for _, id := range selected {
		if strings.TrimSpace(id) == "" {
			return nil, errors.New("moderated channel lookup requires nonempty broadcaster IDs")
		}
		wanted[id] = true
	}
	result := []twitch.ModeratedChannel{}
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	for range maxModeratedChannelPages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parsed, err := url.Parse(c.endpoint)
		if err != nil {
			return nil, credentialSafeUserError("create moderated channels request", err, c.token())
		}
		query := parsed.Query()
		query.Set("user_id", userID)
		query.Set("first", "100")
		if cursor != "" {
			query.Set("after", cursor)
		}
		parsed.RawQuery = query.Encode()
		response, err := getJSON[moderatedChannelsResponse](ctx, c.transport, parsed.String(), errorLabels{action: "get Twitch moderated channels", readAction: "read Twitch moderated channels response", subject: "moderated channels", endpoint: "Get Moderated Channels", channelAPIReasons: map[int]twitch.ChannelAPIReason{http.StatusUnauthorized: twitch.ChannelAPIMissingScope}})
		if err != nil {
			return nil, err
		}
		for _, item := range response.Data {
			if wanted[item.BroadcasterID] && !seenIDs[item.BroadcasterID] {
				seenIDs[item.BroadcasterID] = true
				result = append(result, twitch.ModeratedChannel{BroadcasterID: item.BroadcasterID, BroadcasterLogin: twitch.ChannelKey(item.BroadcasterLogin), BroadcasterName: textsafe.Display(item.BroadcasterName)})
			}
		}
		if len(seenIDs) == len(wanted) {
			return result, nil
		}
		cursor = response.Pagination.Cursor
		if cursor == "" {
			return result, nil
		}
		if len(response.Data) == 0 || len(cursor) > 4096 || seenCursors[cursor] {
			return nil, ErrModeratorProofIncomplete
		}
		seenCursors[cursor] = true
	}
	return nil, ErrModeratorProofIncomplete
}

type moderatedChannelsResponse struct {
	Data []struct {
		BroadcasterID    string `json:"broadcaster_id"`
		BroadcasterLogin string `json:"broadcaster_login"`
		BroadcasterName  string `json:"broadcaster_name"`
	} `json:"data"`
	Pagination struct {
		Cursor string `json:"cursor"`
	} `json:"pagination"`
}
