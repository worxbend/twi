package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/textsafe"
)

const (
	defaultHelixFollowedChannelsURL = "https://api.twitch.tv/helix/channels/followed"
	followedChannelsPageLimit       = 100
	// maxFollowedChannelPages bounds how far the cursor is followed. Twitch
	// returns 100 channels per page, so this covers 1000 follows; the
	// /channels picker is a search box, not a complete directory, and an
	// unbounded loop would hang the picker for accounts that follow
	// thousands of channels.
	maxFollowedChannelPages = 10
)

// FollowedChannel is one channel the authenticated user follows, as reported
// by Twitch Helix "Get Followed Channels" (sorted most-recently-followed
// first).
type FollowedChannel struct {
	BroadcasterID    string
	BroadcasterLogin string
	BroadcasterName  string
	FollowedAt       time.Time
}

// FollowedChannelLookup resolves the channels a user follows. Requires the
// user:read:follows scope, granted by the user for their own account.
type FollowedChannelLookup interface {
	GetFollowedChannels(ctx context.Context, userID string) ([]FollowedChannel, error)
}

// HelixFollowedChannelsClientConfig configures the Twitch Helix followed
// channels adapter. Endpoint and HTTPClient are injectable for deterministic
// fake HTTP tests; zero values use Twitch's production endpoint and the
// default HTTP client.
type HelixFollowedChannelsClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource func() string
	Endpoint         string
	HTTPClient       *http.Client
	ClientID         string
	OAuthToken       string
}

// HelixFollowedChannelsClient resolves the authenticated user's follow list
// through Twitch Helix "Get Followed Channels".
type HelixFollowedChannelsClient struct {
	endpoint string
	helixTransport
}

var _ FollowedChannelLookup = (*HelixFollowedChannelsClient)(nil)

// NewHelixFollowedChannelsClient creates a FollowedChannelLookup backed by
// Twitch Helix HTTP. The returned client performs no network I/O until
// GetFollowedChannels is called.
func NewHelixFollowedChannelsClient(cfg HelixFollowedChannelsClientConfig) *HelixFollowedChannelsClient {
	return &HelixFollowedChannelsClient{
		endpoint:       endpointOrDefault(cfg.Endpoint, defaultHelixFollowedChannelsURL),
		helixTransport: newHelixTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// GetFollowedChannels fetches every channel userID follows, walking Twitch's
// pagination cursor up to maxFollowedChannelPages. Duplicate logins across
// pages are dropped, so a follow list that shifts mid-walk cannot produce
// repeated picker entries.
func (c *HelixFollowedChannelsClient) GetFollowedChannels(ctx context.Context, userID string) ([]FollowedChannel, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("get Twitch followed channels: missing user ID")
	}

	var (
		followed []FollowedChannel
		seen     = make(map[string]bool)
		cursor   string
	)
	for page := 0; page < maxFollowedChannelPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		decoded, err := c.fetchPage(ctx, userID, cursor)
		if err != nil {
			return nil, err
		}
		for _, item := range decoded.Data {
			login := strings.TrimSpace(item.BroadcasterLogin)
			if login == "" || seen[strings.ToLower(login)] {
				continue
			}
			seen[strings.ToLower(login)] = true
			followedAt, _ := time.Parse(time.RFC3339, item.FollowedAt)
			followed = append(followed, FollowedChannel{
				BroadcasterID:    strings.TrimSpace(item.BroadcasterID),
				BroadcasterLogin: login,
				BroadcasterName:  textsafe.Display(strings.TrimSpace(item.BroadcasterName)),
				FollowedAt:       followedAt,
			})
		}
		cursor = strings.TrimSpace(decoded.Pagination.Cursor)
		if cursor == "" || len(decoded.Data) == 0 {
			break
		}
	}
	return followed, nil
}

func (c *HelixFollowedChannelsClient) fetchPage(ctx context.Context, userID, cursor string) (helixFollowedChannelsResponse, error) {
	var empty helixFollowedChannelsResponse

	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return empty, credentialSafeUserError("create Twitch followed channels request", err, c.token())
	}
	q := parsed.Query()
	q.Set("user_id", userID)
	q.Set("first", strconv.Itoa(followedChannelsPageLimit))
	if cursor != "" {
		q.Set("after", cursor)
	}
	parsed.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return empty, credentialSafeUserError("create Twitch followed channels request", err, c.token())
	}
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return empty, credentialSafeUserError("get Twitch followed channels", err, c.token())
	}
	defer resp.Body.Close()

	if !isHelixSuccess(resp) {
		return empty, c.responseError(resp, helixErrorLabels{
			action:     "get Twitch followed channels",
			readAction: "read Twitch followed channels response",
			endpoint:   "Get Followed Channels",
			// A token without user:read:follows fails with 401 "Missing
			// scope", which IsMissingScope recognizes. Either way the picker
			// falls back to open/configured channels rather than treating
			// this as fatal.
			channelAPIStatuses: []int{http.StatusUnauthorized},
		})
	}

	var decoded helixFollowedChannelsResponse
	if err := decodeJSONBody(resp.Body, maxHelixResponseBodySize, &decoded); err != nil {
		return empty, credentialSafeUserError("decode Twitch followed channels response", err, c.token())
	}
	return decoded, nil
}

type helixFollowedChannelsResponse struct {
	Total      int                        `json:"total"`
	Data       []helixFollowedChannelItem `json:"data"`
	Pagination struct {
		Cursor string `json:"cursor"`
	} `json:"pagination"`
}

type helixFollowedChannelItem struct {
	BroadcasterID    string `json:"broadcaster_id"`
	BroadcasterLogin string `json:"broadcaster_login"`
	BroadcasterName  string `json:"broadcaster_name"`
	FollowedAt       string `json:"followed_at"`
}
