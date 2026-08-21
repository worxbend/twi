package twitch

import (
	"context"
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
	endpoint string
	helixTransport
}

var _ GameLookup = (*HelixGamesClient)(nil)

// NewHelixGamesClient creates a GameLookup backed by Twitch Helix HTTP. The
// returned client performs no network I/O until SearchCategories is called.
func NewHelixGamesClient(cfg HelixGamesClientConfig) *HelixGamesClient {
	return &HelixGamesClient{
		endpoint:       endpointOrDefault(cfg.Endpoint, defaultHelixSearchCategoriesURL),
		helixTransport: newHelixTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
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
	c.setAuthHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, credentialSafeUserError("search Twitch categories", err, c.token())
	}
	defer resp.Body.Close()

	if !isHelixSuccess(resp) {
		return nil, c.responseError(resp, helixErrorLabels{
			action:     "search Twitch categories",
			readAction: "read Twitch category search response",
			endpoint:   "Search Categories",
		})
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
