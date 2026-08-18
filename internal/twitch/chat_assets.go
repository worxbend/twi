package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultHelixChannelEmotesURL = "https://api.twitch.tv/helix/chat/emotes"
	defaultHelixGlobalEmotesURL  = "https://api.twitch.tv/helix/chat/emotes/global"
	defaultHelixChannelBadgesURL = "https://api.twitch.tv/helix/chat/badges"
	defaultHelixGlobalBadgesURL  = "https://api.twitch.tv/helix/chat/badges/global"
)

// ChatAssetLookup resolves Twitch emote and badge metadata through a Twitch API
// boundary. Implementations return public image URLs or templates only; callers
// are responsible for caching and downloading image bytes.
type ChatAssetLookup interface {
	GetGlobalEmotes(context.Context) ([]EmoteMetadata, error)
	GetChannelEmotes(context.Context, string) ([]EmoteMetadata, error)
	GetGlobalBadges(context.Context) ([]BadgeMetadata, error)
	GetChannelBadges(context.Context, string) ([]BadgeMetadata, error)
}

// EmoteMetadata contains the Helix fields needed to build a CDN image URL.
type EmoteMetadata struct {
	ID          string
	Name        string
	TemplateURL string
	ImageURL1X  string
	ImageURL2X  string
	ImageURL4X  string
	Formats     []string
	Scales      []string
	ThemeModes  []string
}

// ImageURL returns a deterministic static/light URL when Twitch's template is
// available, falling back to the static URLs Helix includes in each item.
func (m EmoteMetadata) ImageURL() string {
	format := preferredValue(m.Formats, "static")
	theme := preferredValue(m.ThemeModes, "light")
	scale := preferredValue(m.Scales, "2.0")
	if strings.TrimSpace(m.TemplateURL) != "" && strings.TrimSpace(m.ID) != "" && format != "" && theme != "" && scale != "" {
		out := m.TemplateURL
		out = strings.ReplaceAll(out, "{{id}}", m.ID)
		out = strings.ReplaceAll(out, "{{format}}", format)
		out = strings.ReplaceAll(out, "{{theme_mode}}", theme)
		out = strings.ReplaceAll(out, "{{scale}}", scale)
		return out
	}
	if strings.TrimSpace(m.ImageURL2X) != "" {
		return strings.TrimSpace(m.ImageURL2X)
	}
	if strings.TrimSpace(m.ImageURL1X) != "" {
		return strings.TrimSpace(m.ImageURL1X)
	}
	return strings.TrimSpace(m.ImageURL4X)
}

// BadgeMetadata contains one Twitch badge version image.
type BadgeMetadata struct {
	SetID       string
	ID          string
	Title       string
	Description string
	ImageURL1X  string
	ImageURL2X  string
	ImageURL4X  string
}

// ImageURL returns a deterministic medium-size badge URL when present.
func (m BadgeMetadata) ImageURL() string {
	if strings.TrimSpace(m.ImageURL2X) != "" {
		return strings.TrimSpace(m.ImageURL2X)
	}
	if strings.TrimSpace(m.ImageURL1X) != "" {
		return strings.TrimSpace(m.ImageURL1X)
	}
	return strings.TrimSpace(m.ImageURL4X)
}

// HelixChatAssetsClientConfig configures the Twitch Helix chat asset adapter.
// Endpoint fields and HTTPClient are injectable for fake HTTP tests.
type HelixChatAssetsClientConfig struct {
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

// HelixChatAssetsClient resolves Twitch emote and badge metadata through
// Helix chat endpoints.
type HelixChatAssetsClient struct {
	channelEmotesEndpoint string
	globalEmotesEndpoint  string
	channelBadgesEndpoint string
	globalBadgesEndpoint  string
	httpClient            *http.Client
	clientID              string
	oauthTokenSource      func() string
}

var _ ChatAssetLookup = (*HelixChatAssetsClient)(nil)

// NewHelixChatAssetsClient creates a chat asset metadata adapter. It performs
// no network I/O until one of the lookup methods is called.
func NewHelixChatAssetsClient(cfg HelixChatAssetsClientConfig) *HelixChatAssetsClient {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixChatAssetsClient{
		channelEmotesEndpoint: endpointOrDefault(cfg.ChannelEmotesEndpoint, defaultHelixChannelEmotesURL),
		globalEmotesEndpoint:  endpointOrDefault(cfg.GlobalEmotesEndpoint, defaultHelixGlobalEmotesURL),
		channelBadgesEndpoint: endpointOrDefault(cfg.ChannelBadgesEndpoint, defaultHelixChannelBadgesURL),
		globalBadgesEndpoint:  endpointOrDefault(cfg.GlobalBadgesEndpoint, defaultHelixGlobalBadgesURL),
		httpClient:            httpClient,
		clientID:              strings.TrimSpace(cfg.ClientID),
		oauthTokenSource:      resolveTokenSource(cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

func (c *HelixChatAssetsClient) GetGlobalEmotes(ctx context.Context) ([]EmoteMetadata, error) {
	var decoded helixEmotesResponse
	if err := c.getJSON(ctx, c.globalEmotesEndpoint, "", &decoded); err != nil {
		return nil, err
	}
	return decoded.emotes(), nil
}

func (c *HelixChatAssetsClient) GetChannelEmotes(ctx context.Context, broadcasterID string) ([]EmoteMetadata, error) {
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

func (c *HelixChatAssetsClient) GetGlobalBadges(ctx context.Context) ([]BadgeMetadata, error) {
	var decoded helixBadgesResponse
	if err := c.getJSON(ctx, c.globalBadgesEndpoint, "", &decoded); err != nil {
		return nil, err
	}
	return decoded.badges(), nil
}

func (c *HelixChatAssetsClient) GetChannelBadges(ctx context.Context, broadcasterID string) ([]BadgeMetadata, error) {
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

func (c *HelixChatAssetsClient) getJSON(ctx context.Context, endpoint, broadcasterID string, out any) error {
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
	if c.clientID != "" {
		req.Header.Set("Client-Id", c.clientID)
	}
	token := accessTokenForValidation(c.token())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return credentialSafeChatAssetError("lookup Twitch chat assets", err, c.token())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
	if err := decodeJSONBody(resp.Body, maxHelixResponseBodySize, out); err != nil {
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

func (r helixEmotesResponse) emotes() []EmoteMetadata {
	out := make([]EmoteMetadata, 0, len(r.Data))
	for _, item := range r.Data {
		metadata := EmoteMetadata{
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

func (r helixBadgesResponse) badges() []BadgeMetadata {
	var out []BadgeMetadata
	for _, set := range r.Data {
		setID := strings.TrimSpace(set.SetID)
		if setID == "" {
			continue
		}
		for _, version := range set.Versions {
			metadata := BadgeMetadata{
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

func endpointOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func preferredValue(values []string, preferred string) string {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), preferred) {
			return preferred
		}
	}
	if len(values) == 0 {
		return preferred
	}
	return strings.TrimSpace(values[0])
}

func credentialSafeChatAssetError(action string, err error, oauthToken string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	credentials := TokenCredentials{OAuthToken: oauthToken}
	return errors.New(action + ": " + redactCredentials(err.Error(), credentials))
}

// token returns the OAuth token to present on the next request, read now
// rather than captured at construction so a mid-session refresh applies.
func (c *HelixChatAssetsClient) token() string {
	if c == nil || c.oauthTokenSource == nil {
		return ""
	}
	return c.oauthTokenSource()
}
