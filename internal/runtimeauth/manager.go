// Package runtimeauth validates precise capabilities and refreshes headless sessions.
package runtimeauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/helix"
	"github.com/worxbend/twi/internal/twitch/jsonbody"
)

var ErrCredentialStore = errors.New("private credential store failure")

// Reload and Persist must be paired when credentials came from the private store.
// Explicit environment credentials remain in memory and never overwrite saved credentials.
type Config struct {
	Credentials twitch.TokenCredentials
	Required    []auth.Scope
	Validator   twitch.TokenValidator
	HTTPClient  *http.Client
	TokenURL    string
	Reload      func(context.Context) (twitch.TokenCredentials, error)
	Persist     func(context.Context, twitch.TokenCredentials) error
}
type Manager struct {
	mu          sync.RWMutex
	config      Config
	identity    twitch.TokenIdentity
	scopes      []auth.Scope
	validatedAt time.Time
}

func New(cfg Config) *Manager {
	if cfg.Validator == nil {
		cfg.Validator = helix.NewOAuthTokenValidator(helix.OAuthTokenValidatorConfig{HTTPClient: cfg.HTTPClient})
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://id.twitch.tv/oauth2/token"
	}
	return &Manager{config: cfg}
}
func (m *Manager) Token() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Credentials.OAuthToken
}
func (m *Manager) Identity() twitch.TokenIdentity {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.identity
}
func (m *Manager) Validate(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	result, err := m.config.Validator.ValidateToken(ctx, m.config.Credentials)
	if err != nil {
		return err
	}
	if (result.Status == twitch.TokenValidationExpired || result.Status == twitch.TokenValidationMalformed) && m.config.Credentials.RefreshAvailable() {
		if err = m.refreshLocked(ctx); err != nil {
			return err
		}
		return m.validateLocked(ctx)
	}
	return m.applyValidation(result)
}
func (m *Manager) validateLocked(ctx context.Context) error {
	result, err := m.config.Validator.ValidateToken(ctx, m.config.Credentials)
	if err != nil {
		return err
	}
	return m.applyValidation(result)
}
func (m *Manager) applyValidation(result twitch.TokenValidationResult) error {
	if m.identity.UserID != "" && result.Identity.UserID != m.identity.UserID {
		return fmt.Errorf("OAuth token account changed during session: %w", twitch.ErrAuthFailed)
	}
	if twitch.LoginMismatch(m.config.Credentials.Username, result.Identity.Login) {
		return fmt.Errorf("OAuth token belongs to another account: %w", twitch.ErrAuthFailed)
	}
	if result.ClientID != "" && m.config.Credentials.ClientID != "" && result.ClientID != m.config.Credentials.ClientID {
		return fmt.Errorf("OAuth token belongs to another client application: %w", twitch.ErrAuthFailed)
	}
	if result.Status != twitch.TokenValidationValid && result.Status != twitch.TokenValidationMissingScope {
		return fmt.Errorf("OAuth token invalid; run twi login for required capability: %w", twitch.ErrAuthFailed)
	}
	if missing := auth.MissingScopes(result.Scopes, m.config.Required); len(missing) > 0 {
		return fmt.Errorf("auth_scope_missing: requires %v; run twi login --capability chat-read, chat-send, or eventsub-read: %w", auth.ScopeValues(missing), twitch.ErrAuthFailed)
	}
	if m.config.Credentials.Username == "" {
		m.config.Credentials.Username = result.Identity.Login
	}
	m.identity = result.Identity
	m.scopes = append([]auth.Scope(nil), result.Scopes...)
	m.validatedAt = time.Now()
	return nil
}
func (m *Manager) ValidateIfDue(ctx context.Context) error {
	m.mu.RLock()
	due := time.Since(m.validatedAt) >= time.Hour
	m.mu.RUnlock()
	if due {
		return m.Validate(ctx)
	}
	return nil
}

// Refresh is only safe after an explicit authentication rejection, never an ambiguous send response.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.refreshLocked(ctx); err != nil {
		return err
	}
	return m.validateLocked(ctx)
}
func (m *Manager) refreshLocked(ctx context.Context) error {
	previous := m.config.Credentials
	if !previous.RefreshAvailable() {
		return fmt.Errorf("OAuth refresh unavailable; run twi login for required capability: %w", twitch.ErrAuthFailed)
	}
	return sendquota.WithAccountLock(ctx, "auth-lock", previous.ClientID+":"+strings.ToLower(previous.Username), func() error {
		if m.config.Reload != nil {
			latest, err := m.config.Reload(ctx)
			if err != nil {
				return fmt.Errorf("cannot reload private credentials: %w", ErrCredentialStore)
			}
			if latest.OAuthToken != "" && latest.OAuthToken != previous.OAuthToken {
				m.config.Credentials = latest
				return nil
			}
		}
		values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {previous.RefreshToken}, "client_id": {previous.ClientID}, "client_secret": {previous.ClientSecret}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.config.TokenURL, strings.NewReader(values.Encode()))
		if err != nil {
			return errors.New("cannot create OAuth refresh request")
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := m.config.HTTPClient.Do(req)
		if err != nil {
			return errors.New("OAuth refresh request failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest {
				return fmt.Errorf("OAuth refresh rejected (HTTP %d); run twi login: %w", resp.StatusCode, twitch.ErrAuthFailed)
			}
			return fmt.Errorf("OAuth refresh endpoint unavailable (HTTP %d)", resp.StatusCode)
		}
		var result struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		}
		if err = jsonbody.Decode(resp.Body, 64*1024, &result); err != nil || result.AccessToken == "" {
			return errors.New("invalid OAuth refresh response")
		}
		refreshed := previous
		refreshed.OAuthToken = twitch.NormalizeIRCOAuthToken(result.AccessToken)
		if result.RefreshToken != "" {
			refreshed.RefreshToken = result.RefreshToken
		}
		m.config.Credentials = refreshed
		if m.config.Persist != nil {
			if err = m.config.Persist(ctx, refreshed); err != nil {
				return fmt.Errorf("refreshed token active but private credential persistence failed: %w", ErrCredentialStore)
			}
		}
		return nil
	})
}

// Credentials returns a private snapshot for a transport adapter. Never print it.
func (m *Manager) Credentials() twitch.TokenCredentials {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Credentials
}

// Scopes returns a copy of currently validated grants for optional reader capabilities.
func (m *Manager) Scopes() []auth.Scope {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]auth.Scope(nil), m.scopes...)
}
