// Package runtimeapp owns explicit app-token sending authorization. App tokens
// remain in memory and are isolated from saved user tokens and refresh grants.
package runtimeapp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/helix"
)

type TokenProvider interface {
	Acquire(context.Context) (auth.Secret, time.Time, error)
	Validate(context.Context, auth.Secret) error
}
type GrantLookup interface {
	GetGrants(context.Context, []string) ([]helix.AuthorizationGrant, error)
}
type Config struct {
	Tokens     TokenProvider
	Grants     GrantLookup
	Moderators twitch.ModeratedChannelLookup
	Sender     twitch.TokenIdentity
}
type Manager struct {
	mu                 sync.RWMutex
	config             Config
	token              auth.Secret
	expires, validated time.Time
	moderators         map[string]bool
}

func New(config Config) *Manager { return &Manager{config: config, moderators: map[string]bool{}} }
func (m *Manager) Token() string { m.mu.RLock(); defer m.mu.RUnlock(); return m.token.Reveal() }
func (m *Manager) Identity() twitch.TokenIdentity {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Sender
}
func (m *Manager) SetSender(sender twitch.TokenIdentity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Sender = sender
}
func (m *Manager) SetAuthority(grants GrantLookup, moderators twitch.ModeratedChannelLookup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Grants = grants
	m.config.Moderators = moderators
}
func (m *Manager) ValidateIfDue(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.token.Present() || time.Until(m.expires) < time.Minute {
		return m.refreshLocked(ctx)
	}
	if time.Since(m.validated) >= time.Hour {
		if err := m.config.Tokens.Validate(ctx, m.token); err != nil {
			return m.refreshLocked(ctx)
		}
		m.validated = time.Now()
	}
	return nil
}
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshLocked(ctx)
}
func (m *Manager) refreshLocked(ctx context.Context) error {
	token, expires, err := m.config.Tokens.Acquire(ctx)
	if err != nil {
		return err
	}
	m.token = token
	m.expires = expires
	m.validated = time.Now()
	return nil
}

// AuthorizeDestination rechecks remote grants/roles before every application send.
// A granted bot scope alone never proves permission in a broadcaster's room.
func (m *Manager) AuthorizeDestination(ctx context.Context, broadcasterID string) error {
	m.mu.RLock()
	sender, lookup, roles := m.config.Sender, m.config.Grants, m.config.Moderators
	m.mu.RUnlock()
	if sender.UserID == "" || lookup == nil {
		return fmt.Errorf("app sender identity/authority missing: %w", twitch.ErrAuthFailed)
	}
	ids := []string{sender.UserID}
	if broadcasterID != sender.UserID {
		ids = append(ids, broadcasterID)
	}
	grants, err := lookup.GetGrants(ctx, ids)
	if err != nil {
		return err
	}
	byID := map[string]helix.AuthorizationGrant{}
	for _, grant := range grants {
		byID[grant.UserID] = grant
	}
	bot := byID[sender.UserID]
	if !bot.Authorized || !has(bot.Scopes, "user:write:chat") || !has(bot.Scopes, "user:bot") {
		return fmt.Errorf("sender must authorize user:write:chat and user:bot for this app: %w", twitch.ErrAuthFailed)
	}
	broadcaster := byID[broadcasterID]
	channelBot := broadcaster.Authorized && has(broadcaster.Scopes, "channel:bot")
	verified := false
	if roles != nil && has(bot.Scopes, "user:read:moderated_channels") && sender.UserID != broadcasterID {
		proof, err := roles.GetModeratedChannels(ctx, sender.UserID, []string{broadcasterID})
		if err != nil && !channelBot {
			return err
		}
		verified = err == nil && len(proof) == 1 && proof[0].BroadcasterID == broadcasterID
	}
	m.mu.Lock()
	if len(m.moderators) >= 100 {
		clear(m.moderators)
	}
	m.moderators[broadcasterID] = verified
	m.mu.Unlock()
	if channelBot || verified {
		return nil
	}
	return fmt.Errorf("broadcaster must authorize channel:bot or sender must have verified moderator status: %w", twitch.ErrAuthFailed)
}
func (m *Manager) ModeratorProof(_ context.Context, id string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.moderators[id], nil
}
func has(scopes []auth.Scope, wanted auth.Scope) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}
