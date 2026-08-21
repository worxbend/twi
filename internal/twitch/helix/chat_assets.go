package helix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/worxbend/twi/internal/twitch"
)

const (
	defaultHelixChannelEmotesURL = "https://api.twitch.tv/helix/chat/emotes"
	defaultHelixGlobalEmotesURL  = "https://api.twitch.tv/helix/chat/emotes/global"
	defaultHelixChannelBadgesURL = "https://api.twitch.tv/helix/chat/badges"
	defaultHelixGlobalBadgesURL  = "https://api.twitch.tv/helix/chat/badges/global"
)

// ChatAssetsClientConfig configures the Twitch Helix chat asset adapter.
// Endpoint fields and HTTPClient are injectable for fake HTTP tests.
type ChatAssetsClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource      func() string
	ChannelEmotesEndpoint string
	GlobalEmotesEndpoint  string
	ChannelBadgesEndpoint string
	GlobalBadgesEndpoint  string
	HTTPClient            *http.Client
	ClientID              string
	OAuthToken            string
}

// ChatAssetsClient resolves Twitch emote and badge metadata through
// Helix chat endpoints.
type ChatAssetsClient struct {
	channelEmotesEndpoint string
	globalEmotesEndpoint  string
	channelBadgesEndpoint string
	globalBadgesEndpoint  string
	transport
}

var _ twitch.ChatAssetLookup = (*ChatAssetsClient)(nil)

// NewChatAssetsClient creates a chat asset metadata adapter. It performs
// no network I/O until one of the lookup methods is called.
func NewChatAssetsClient(cfg ChatAssetsClientConfig) *ChatAssetsClient {
	return &ChatAssetsClient{
		channelEmotesEndpoint: endpointOrDefault(cfg.ChannelEmotesEndpoint, defaultHelixChannelEmotesURL),
		globalEmotesEndpoint:  endpointOrDefault(cfg.GlobalEmotesEndpoint, defaultHelixGlobalEmotesURL),
		channelBadgesEndpoint: endpointOrDefault(cfg.ChannelBadgesEndpoint, defaultHelixChannelBadgesURL),
		globalBadgesEndpoint:  endpointOrDefault(cfg.GlobalBadgesEndpoint, defaultHelixGlobalBadgesURL),
		transport:             newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

func (c *ChatAssetsClient) GetGlobalEmotes(ctx context.Context) ([]twitch.EmoteMetadata, error) {
	var decoded helixEmotesResponse
	if err := c.getJSON(ctx, c.globalEmotesEndpoint, "", &decoded); err != nil {
		return nil, err
	}
	return decoded.emotes(), nil
}

func (c *ChatAssetsClient) GetChannelEmotes(ctx context.Context, broadcasterID string) ([]twitch.EmoteMetadata, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return nil, nil
	}
	var decoded helixEmotesResponse
	if err := c.getJSON(ctx, c.channelEmotesEndpoint, broadcasterID, &decoded); err != nil {
		return nil, err
	}
	return decoded.emotes(), nil
}

func (c *ChatAssetsClient) GetGlobalBadges(ctx context.Context) ([]twitch.BadgeMetadata, error) {
	var decoded helixBadgesResponse
	if err := c.getJSON(ctx, c.globalBadgesEndpoint, "", &decoded); err != nil {
		return nil, err
	}
	return decoded.badges(), nil
}

func (c *ChatAssetsClient) GetChannelBadges(ctx context.Context, broadcasterID string) ([]twitch.BadgeMetadata, error) {
	broadcasterID = strings.TrimSpace(broadcasterID)
	if broadcasterID == "" {
		return nil, nil
	}
	var decoded helixBadgesResponse
	if err := c.getJSON(ctx, c.channelBadgesEndpoint, broadcasterID, &decoded); err != nil {
		return nil, err
	}
	return decoded.badges(), nil
}

func (c *ChatAssetsClient) getJSON(ctx context.Context, endpoint, broadcasterID string, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	endpoint, err := helixChatAssetURL(endpoint, broadcasterID)
	if err != nil {
		return credentialSafeChatAssetError("create Twitch chat asset request", err, c.token())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return credentialSafeChatAssetError("create Twitch chat asset request", err, c.token())
	}
	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return credentialSafeChatAssetError("lookup Twitch chat assets", err, c.token())
	}
	defer resp.Body.Close()

	if !isSuccess(resp) {
		detail, err := readSmallBody(resp.Body)
		if err != nil {
			return credentialSafeChatAssetError("read Twitch chat asset response", err, c.token())
		}
		if detail != "" {
			detail = ": " + detail
		}
		return credentialSafeChatAssetError(
			"lookup Twitch chat assets",
			fmt.Errorf("twitch chat asset lookup returned HTTP %d%s", resp.StatusCode, detail),
			c.token(),
		)
	}
	if err := decodeJSONBody(resp.Body, maxResponseBodySize, out); err != nil {
		return credentialSafeChatAssetError("decode Twitch chat asset response", err, c.token())
	}
	return nil
}

func helixChatAssetURL(endpoint, broadcasterID string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if broadcasterID != "" {
		query := parsed.Query()
		query.Set("broadcaster_id", broadcasterID)
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

type helixEmotesResponse struct {
	Data     []helixEmote `json:"data"`
	Template string       `json:"template"`
}

type helixEmote struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Images    helixEmoteImages `json:"images"`
	Formats   []string         `json:"format"`
	Scales    []string         `json:"scale"`
	ThemeMode []string         `json:"theme_mode"`
}

type helixEmoteImages struct {
	URL1X string `json:"url_1x"`
	URL2X string `json:"url_2x"`
	URL4X string `json:"url_4x"`
}

func (r helixEmotesResponse) emotes() []twitch.EmoteMetadata {
	out := make([]twitch.EmoteMetadata, 0, len(r.Data))
	for _, item := range r.Data {
		metadata := twitch.EmoteMetadata{
			ID:          strings.TrimSpace(item.ID),
			Name:        strings.TrimSpace(item.Name),
			TemplateURL: strings.TrimSpace(r.Template),
			ImageURL1X:  strings.TrimSpace(item.Images.URL1X),
			ImageURL2X:  strings.TrimSpace(item.Images.URL2X),
			ImageURL4X:  strings.TrimSpace(item.Images.URL4X),
			Formats:     uniqueNonEmpty(item.Formats),
			Scales:      uniqueNonEmpty(item.Scales),
			ThemeModes:  uniqueNonEmpty(item.ThemeMode),
		}
		if metadata.ID == "" || metadata.ImageURL() == "" {
			continue
		}
		out = append(out, metadata)
	}
	return out
}

type helixBadgesResponse struct {
	Data []helixBadgeSet `json:"data"`
}

type helixBadgeSet struct {
	SetID    string              `json:"set_id"`
	Versions []helixBadgeVersion `json:"versions"`
}

type helixBadgeVersion struct {
	ID          string `json:"id"`
	ImageURL1X  string `json:"image_url_1x"`
	ImageURL2X  string `json:"image_url_2x"`
	ImageURL4X  string `json:"image_url_4x"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (r helixBadgesResponse) badges() []twitch.BadgeMetadata {
	var out []twitch.BadgeMetadata
	for _, set := range r.Data {
		setID := strings.TrimSpace(set.SetID)
		if setID == "" {
			continue
		}
		for _, version := range set.Versions {
			metadata := twitch.BadgeMetadata{
				SetID:       setID,
				ID:          strings.TrimSpace(version.ID),
				Title:       strings.TrimSpace(version.Title),
				Description: strings.TrimSpace(version.Description),
				ImageURL1X:  strings.TrimSpace(version.ImageURL1X),
				ImageURL2X:  strings.TrimSpace(version.ImageURL2X),
				ImageURL4X:  strings.TrimSpace(version.ImageURL4X),
			}
			if metadata.ID == "" || metadata.ImageURL() == "" {
				continue
			}
			out = append(out, metadata)
		}
	}
	return out
}

func credentialSafeChatAssetError(action string, err error, oauthToken string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	credentials := twitch.TokenCredentials{OAuthToken: oauthToken}
	return errors.New(action + ": " + redactCredentials(err.Error(), credentials))
}
