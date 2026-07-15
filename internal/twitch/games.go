package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const defaultHelixGamesURL = "https://api.twitch.tv/helix/games"

// Game identifies a Twitch category/game.
type Game struct {
	ID   string
	Name string
}

// GameLookup resolves a Twitch category/game by its exact display name, the
// form Twitch Helix "Modify Channel Information" requires for changing a
// channel's category.
type GameLookup interface {
	GetGameByName(ctx context.Context, name string) (Game, bool, error)
}

// HelixGamesClientConfig configures the Twitch Helix Get Games adapter.
// Endpoint and HTTPClient are injectable for deterministic fake HTTP tests;
// zero values use Twitch's production endpoint and the default HTTP client.
type HelixGamesClientConfig struct {
	Endpoint   string
	HTTPClient *http.Client
	ClientID   string
	OAuthToken string
}

// HelixGamesClient resolves Twitch categories/games through Helix Get Games.
type HelixGamesClient struct {
	endpoint   string
	httpClient *http.Client
	clientID   string
	oauthToken string
}

var _ GameLookup = (*HelixGamesClient)(nil)

// NewHelixGamesClient creates a GameLookup backed by Twitch Helix HTTP. The
// returned client performs no network I/O until GetGameByName is called.
func NewHelixGamesClient(cfg HelixGamesClientConfig) *HelixGamesClient {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultHelixGamesURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HelixGamesClient{
		endpoint:   endpoint,
		httpClient: httpClient,
		clientID:   strings.TrimSpace(cfg.ClientID),
		oauthToken: strings.TrimSpace(cfg.OAuthToken),
	}
}

// GetGameByName performs one Helix Get Games request for the exact category
// name. The bool result reports whether a matching category was found; a
// false result with a nil error means Twitch has no category with that exact
// name.
func (c *HelixGamesClient) GetGameByName(ctx context.Context, name string) (Game, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Game{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return Game{}, false, err
	}

	parsed, err := url.Parse(c.endpoint)
	if err != nil {
		return Game{}, false, credentialSafeUserError("create Twitch category lookup request", err, c.oauthToken)
	}
	query := parsed.Query()
	query.Set("name", name)
	parsed.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Game{}, false, credentialSafeUserError("create Twitch category lookup request", err, c.oauthToken)
	}
	if c.clientID != "" {
		httpReq.Header.Set("Client-Id", c.clientID)
	}
	token := accessTokenForValidation(c.oauthToken)
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Game{}, false, credentialSafeUserError("lookup Twitch category", err, c.oauthToken)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, readErr := readSmallBody(resp.Body)
		if readErr != nil {
			return Game{}, false, credentialSafeUserError("read Twitch category lookup response", readErr, c.oauthToken)
		}
		if detail != "" {
			detail = ": " + detail
		}
		return Game{}, false, credentialSafeUserError(
			"lookup Twitch category",
			fmt.Errorf("twitch Get Games returned HTTP %d%s", resp.StatusCode, detail),
			c.oauthToken,
		)
	}

	var decoded helixGamesResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Game{}, false, credentialSafeUserError("decode Twitch category lookup response", err, c.oauthToken)
	}
	for _, item := range decoded.Data {
		if strings.EqualFold(strings.TrimSpace(item.Name), name) {
			return Game{ID: strings.TrimSpace(item.ID), Name: strings.TrimSpace(item.Name)}, true, nil
		}
	}
	return Game{}, false, nil
}

type helixGamesResponse struct {
	Data []helixGame `json:"data"`
}

type helixGame struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
