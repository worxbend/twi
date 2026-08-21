package helix

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	defaultHelixSearchCategoriesURL = "https://api.twitch.tv/helix/search/categories"
	defaultCategorySearchLimit      = 20
	maxCategorySearchLimit          = 100
)

// GamesClient searches Twitch categories/games through Helix Search
// Categories.
type GamesClient struct {
	endpoint string
	transport
}

var _ twitch.GameLookup = (*GamesClient)(nil)

// NewGamesClient creates a GameLookup backed by Twitch Helix HTTP. The
// returned client performs no network I/O until SearchCategories is called.
func NewGamesClient(cfg GamesClientConfig) *GamesClient {
	return &GamesClient{
		endpoint:  endpointOrDefault(cfg.Endpoint, defaultHelixSearchCategoriesURL),
		transport: newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken),
	}
}

// SearchCategories performs one Helix Search Categories request for query,
// returning up to limit fuzzy-matched categories in Twitch's relevance
// order. A blank query skips network I/O and returns no results, since
// Twitch's search endpoint requires a non-empty query.
func (c *GamesClient) SearchCategories(ctx context.Context, query string, limit int) ([]twitch.Game, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit = clampLimit(limit, defaultCategorySearchLimit, maxCategorySearchLimit)

	endpoint, err := queryURL(c.endpoint, url.Values{
		"query": {query},
		"first": {strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, credentialSafeUserError("create Twitch category search request", err, c.token())
	}

	decoded, err := getJSON[helixGamesResponse](ctx, c.transport, endpoint, errorLabels{
		action:     "search Twitch categories",
		readAction: "read Twitch category search response",
		endpoint:   "Search Categories",
	})
	if err != nil {
		return nil, err
	}
	games := make([]twitch.Game, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		id := strings.TrimSpace(item.ID)
		name := textsafe.Display(strings.TrimSpace(item.Name))
		if id == "" || name == "" {
			continue
		}
		games = append(games, twitch.Game{ID: id, Name: name})
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
