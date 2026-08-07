package cli

import (
	"sync"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

// credentialHolder is the current Twitch credentials for the process.
//
// It exists because a refresh rotates both tokens, and everything that had
// copied the old ones is then holding credentials Twitch has invalidated. The
// IRC transport factory used to close over a config.Config by value, so
// ctrl+r -- the key the UI tells you to press when chat drops -- rebuilt the
// client with the pre-refresh access token and the already-rotated refresh
// token. The documented recovery path was the thing that made the session
// unrecoverable, and the only way back was restarting twi.
//
// Reads take an RLock and happen once per connection attempt, so contention is
// irrelevant; correctness across the refresh is the whole point.
type credentialHolder struct {
	mu    sync.RWMutex
	creds config.TwitchConfig
}

func newCredentialHolder(creds config.TwitchConfig) *credentialHolder {
	return &credentialHolder{creds: creds}
}

// current returns a snapshot of the live credentials.
func (h *credentialHolder) current() config.TwitchConfig {
	if h == nil {
		return config.TwitchConfig{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.creds
}

// applyRefresh records freshly minted tokens.
//
// Twitch may or may not rotate the refresh token; when it does not, the
// response carries an empty one and the existing value stays valid, so an
// empty field must never overwrite what is held.
func (h *credentialHolder) applyRefresh(refreshed twitch.OAuthRefresh) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if token := refreshed.AccessToken.Reveal(); token != "" {
		h.creds.OAuthToken = token
	}
	if refresh := refreshed.RefreshToken.Reveal(); refresh != "" {
		h.creds.RefreshToken = refresh
	}
}

// configWithCurrentCredentials returns cfg with its Twitch credentials
// replaced by the live ones, for callers that need a whole config.
func (h *credentialHolder) configWithCurrentCredentials(cfg config.Config) config.Config {
	cfg.Twitch = h.current()
	return cfg
}

// tokenSource returns an accessor for the current access token, suitable for
// a Helix client's OAuthTokenSource. A nil holder yields nil, which leaves
// those clients on their static configured token.
func (h *credentialHolder) tokenSource() func() string {
	if h == nil {
		return nil
	}
	return func() string { return h.current().OAuthToken }
}
