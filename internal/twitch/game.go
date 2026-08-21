package twitch

import (
	"context"
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
