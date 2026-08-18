package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/worxbend/twi/internal/textsafe"
)

const (
	defaultHelixSearchCategoriesURL = "https://api.twitch.tv/helix/search/categories"
	defaultCategorySearchLimit      = 20
	maxCategorySearchLimit          = 100
)

// Game identifies a Twitch category/game.
type Game struct {
	ID   string
	Name string
}

// GameLookup searches Twitch categories/games by a user-typed query, so the
// Stream Info tab can offer a select-from-API picker instead of free-text
// category entry (Twitch's Modify Channel Information endpoint requires a
// game_id, not a display name, and only real categories are valid).
type GameLookup interface {
	SearchCategories(ctx context.Context, query string, limit int) ([]Game, error)
}

// HelixGamesClientConfig configures the Twitch Helix category search
// adapter. Endpoint and HTTPClient are injectable for deterministic fake
// HTTP tests; zero values use Twitch's production endpoint and the default
// HTTP client.
type HelixGamesClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource func() string
	Endpoint         string
	HTTPClient       *http.Client
	ClientID         string
	OAuthToken       string
}

// HelixGamesClient searches Twitch categories/games through Helix Search
// Categories.
type HelixGamesClient struct {
	endpoint         string
	httpClient       *http.Client
	clientID         string
	oauthTokenSource func() string
}

var _ GameLookup = (*HelixGamesClient)(nil)

// NewHelixGamesClient creates a GameLookup backed by Twitch Helix HTTP. The
// returned client performs no network I/O until SearchCategories is called.
func NewHelixGamesClient(cfg HelixGamesClientConfig) *HelixGamesClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultHelixSearchCategoriesURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixGamesClient{
		endpoint:         endpoint,
		httpClient:       httpClient,
		clientID:         strings.TrimSpace(cfg.ClientID),
		oauthTokenSource: resolveTokenSource(cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// SearchCategories performs one Helix Search Categories request for query,
// returning up to limit fuzzy-matched categories in Twitch's relevance
// order. A blank query skips network I/O and returns no results, since
// Twitch's search endpoint requires a non-empty query.
func (c *HelixGamesClient) SearchCategories(ctx context.Context, query string, limit int) ([]Game, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultCategorySearchLimit
	}
	if limit > maxCategorySearchLimit {
		limit = maxCategorySearchLimit
	}

	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch category search request", err, c.token())
	}
	q := parsed.Query()
	q.Set("query", query)
	q.Set("first", strconv.Itoa(limit))
	parsed.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, credentialSafeUserError("create Twitch category search request", err, c.token())
	}
	if c.clientID != "" {
		httpReq.Header.Set("Client-Id", c.clientID)
	}
	token := accessTokenForValidation(c.token())
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, credentialSafeUserError("search Twitch categories", err, c.token())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, readErr := readSmallBody(resp.Body)
		if readErr != nil {
			return nil, credentialSafeUserError("read Twitch category search response", readErr, c.token())
		}
		if detail != "" {
			detail = ": " + detail
		}
		return nil, credentialSafeUserError(
			"search Twitch categories",
			fmt.Errorf("twitch Search Categories returned HTTP %d%s", resp.StatusCode, detail),
			c.token(),
		)
	}

	var decoded helixGamesResponse
	if err := decodeJSONBody(resp.Body, maxHelixResponseBodySize, &decoded); err != nil {
		return nil, credentialSafeUserError("decode Twitch category search response", err, c.token())
	}
	games := make([]Game, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		id := strings.TrimSpace(item.ID)
		name := textsafe.Display(strings.TrimSpace(item.Name))
		if id == "" || name == "" {
			continue
		}
		games = append(games, Game{ID: id, Name: name})
	}
	return games, nil
}

type helixGamesResponse struct {
	Data []helixGame `json:"data"`
}

type helixGame struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// token returns the OAuth token to present on the next request, read now
// rather than captured at construction so a mid-session refresh applies.
func (c *HelixGamesClient) token() string {
	if c == nil || c.oauthTokenSource == nil {
		return ""
	}
	return c.oauthTokenSource()
}
